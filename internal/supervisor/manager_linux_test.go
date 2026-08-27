//go:build linux

package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestManagerStartsAndStopsProcessGroup(t *testing.T) {
	t.Parallel()
	binary := buildFakeSingBox(t)
	manager := NewManager(binary)

	startCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snapshot, err := manager.Start(startCtx, filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if snapshot.State != StateRunning || snapshot.PID == 0 {
		t.Fatalf("Start() snapshot = %+v", snapshot)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	snapshot, err = manager.Stop(stopCtx)
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if snapshot.State != StateStopped || snapshot.PID != 0 {
		t.Fatalf("Stop() snapshot = %+v", snapshot)
	}
}

func TestManagerRestartsAfterCrash(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "starts")
	binary := writeScript(t, `
count=0
if [ -f "$3" ]; then count=$(cat "$3"); fi
count=$((count + 1))
printf '%s' "$count" > "$3"
if [ "$count" -lt 3 ]; then exit 1; fi
trap 'exit 0' TERM INT
while :; do sleep 1; done
`)
	manager := newManagerWithPolicy(binary, testRestartPolicy(3, 10*time.Millisecond))
	t.Cleanup(func() { stopManager(t, manager) })

	snapshot, err := manager.Start(context.Background(), counter)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if snapshot.State != StateRunning || snapshot.Generation != 1 || snapshot.PID == 0 {
		t.Fatalf("Start() snapshot = %+v", snapshot)
	}
	if got := readCount(t, counter); got != 3 {
		t.Fatalf("process start count = %d, want 3", got)
	}
}

func TestManagerFailsAfterRestartBudgetIsExhausted(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "starts")
	binary := writeScript(t, `
count=0
if [ -f "$3" ]; then count=$(cat "$3"); fi
count=$((count + 1))
printf '%s' "$count" > "$3"
exit 1
`)
	manager := newManagerWithPolicy(binary, testRestartPolicy(2, 10*time.Millisecond))

	snapshot, err := manager.Start(context.Background(), counter)
	if err == nil {
		t.Fatal("Start() succeeded after every process crashed")
	}
	if snapshot.State != StateFailed || snapshot.PID != 0 || snapshot.LastError == "" {
		t.Fatalf("Start() snapshot = %+v", snapshot)
	}
	if got := readCount(t, counter); got != 3 {
		t.Fatalf("process start count = %d, want initial start plus 2 restarts", got)
	}
}

func TestManagerStopCancelsPendingRestart(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "starts")
	binary := writeScript(t, `
count=0
if [ -f "$3" ]; then count=$(cat "$3"); fi
count=$((count + 1))
printf '%s' "$count" > "$3"
exit 1
`)
	manager := newManagerWithPolicy(binary, testRestartPolicy(3, 500*time.Millisecond))
	startDone := make(chan error, 1)
	go func() {
		_, err := manager.Start(context.Background(), counter)
		startDone <- err
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot := manager.Snapshot()
		if snapshot.State == StateStarting && snapshot.PID == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if snapshot := manager.Snapshot(); snapshot.State != StateStarting || snapshot.PID != 0 {
		t.Fatalf("manager did not enter restart backoff: %+v", snapshot)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snapshot, err := manager.Stop(stopCtx)
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if snapshot.State != StateStopped {
		t.Fatalf("Stop() snapshot = %+v", snapshot)
	}
	select {
	case err := <-startDone:
		if err != nil {
			t.Fatalf("concurrent Start() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start() did not return after Stop() canceled restart")
	}
	time.Sleep(600 * time.Millisecond)
	if got := readCount(t, counter); got != 1 {
		t.Fatalf("process start count after Stop() = %d, want 1", got)
	}
}

func TestManagerResetsRestartBudgetAfterStableRun(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "starts")
	binary := writeScript(t, `
count=0
if [ -f "$3" ]; then count=$(cat "$3"); fi
count=$((count + 1))
printf '%s' "$count" > "$3"
if [ "$count" -eq 1 ]; then exit 1; fi
if [ "$count" -eq 2 ]; then sleep 0.08; exit 1; fi
trap 'exit 0' TERM INT
while :; do sleep 1; done
`)
	policy := testRestartPolicy(1, 10*time.Millisecond)
	policy.stableWindow = 50 * time.Millisecond
	manager := newManagerWithPolicy(binary, policy)
	t.Cleanup(func() { stopManager(t, manager) })

	snapshot, err := manager.Start(context.Background(), counter)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if snapshot.State != StateRunning || snapshot.LastError != "" {
		t.Fatalf("Start() snapshot = %+v", snapshot)
	}
	if got := readCount(t, counter); got != 3 {
		t.Fatalf("process start count = %d, want budget reset to permit third start", got)
	}
}

func testRestartPolicy(maxRestarts int, initialDelay time.Duration) restartPolicy {
	return restartPolicy{
		maxRestarts:  maxRestarts,
		initialDelay: initialDelay,
		maxDelay:     time.Second,
		stableWindow: time.Minute,
	}
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "fake-sing-box")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}

func readCount(t *testing.T, path string) int {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	count, err := strconv.Atoi(string(content))
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func stopManager(t *testing.T, manager *Manager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := manager.Stop(ctx); err != nil {
		t.Errorf("Stop() cleanup error = %v", err)
	}
}

func buildFakeSingBox(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "fake-sing-box")
	command := exec.Command("go", "build", "-o", binary, "../../testdata/fake-sing-box")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fake sing-box: %v\n%s", err, output)
	}
	return binary
}
