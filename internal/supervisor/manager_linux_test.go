//go:build linux

package supervisor

import (
	"context"
	"os/exec"
	"path/filepath"
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

func buildFakeSingBox(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "fake-sing-box")
	command := exec.Command("go", "build", "-o", binary, "../../testdata/fake-sing-box")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fake sing-box: %v\n%s", err, output)
	}
	return binary
}
