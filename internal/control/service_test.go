//go:build linux

package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"sing-box-webui/internal/supervisor"
)

type fakeSystemProxy struct {
	mu         sync.Mutex
	restoreErr error
	restores   int
}

func (p *fakeSystemProxy) Available(context.Context) (bool, string)    { return true, "available" }
func (p *fakeSystemProxy) Apply(context.Context, string, uint16) error { return nil }
func (p *fakeSystemProxy) Restore(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.restores++
	return p.restoreErr
}

func (p *fakeSystemProxy) restoreCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.restores
}

func TestStopTerminatesCoreWhenProxyRestoreFails(t *testing.T) {
	t.Parallel()
	manager := startTestSupervisor(t, "while :; do sleep 1; done")
	proxy := &fakeSystemProxy{restoreErr: errors.New("restore unavailable")}
	service := &Service{
		supervisor:  manager,
		systemProxy: proxy,
		runtime:     Runtime{State: supervisor.StateRunning},
	}
	if _, err := service.Stop(context.Background()); err == nil {
		t.Fatal("Stop() succeeded despite proxy restore failure")
	}
	if state := manager.Snapshot().State; state != supervisor.StateStopped {
		t.Fatalf("supervisor state = %q, want stopped", state)
	}
	time.Sleep(500 * time.Millisecond)
	if got := proxy.restoreCount(); got != 1 {
		t.Fatalf("system proxy restore count = %d, want 1", got)
	}
	if runtime := service.Status(context.Background()); runtime.State != supervisor.StateFailed || runtime.LastError == "" {
		t.Fatalf("runtime after failed cleanup = %+v, want failed state with error", runtime)
	}
}

func TestSupervisorWatcherCleansUpUnexpectedExit(t *testing.T) {
	t.Parallel()
	manager := startTestSupervisor(t, "sleep 1; exit 1")
	proxy := &fakeSystemProxy{}
	service := &Service{
		supervisor:  manager,
		systemProxy: proxy,
		runtime:     Runtime{State: supervisor.StateRunning},
	}
	generation := manager.Snapshot().Generation
	go service.watchSupervisor(generation)
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		service.mu.RLock()
		state := service.runtime.State
		service.mu.RUnlock()
		if state == supervisor.StateFailed && proxy.restoreCount() > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("watcher did not clean up: runtime=%+v restores=%d", service.runtime, proxy.restoreCount())
}

func startTestSupervisor(t *testing.T, body string) *supervisor.Manager {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "fake-sing-box")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := supervisor.NewManager(binary)
	if _, err := manager.Start(context.Background(), filepath.Join(t.TempDir(), "config.json")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = manager.Stop(ctx)
	})
	return manager
}
