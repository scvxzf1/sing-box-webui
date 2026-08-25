//go:build linux

package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sing-box-webui/internal/supervisor"
)

func TestHotSwitchChangesRootSelectorAndPreservesRuntimeStart(t *testing.T) {
	t.Parallel()
	selected := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut || request.URL.Path != "/proxies/proxy" || request.Header.Get("Authorization") != "Bearer secret" {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		var input map[string]string
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		selected <- input["name"]
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	startedAt := time.Now().Add(-time.Hour).UTC()
	service := &Service{
		runtime:           Runtime{State: supervisor.StateRunning, Mode: "tun", TargetType: "node", SubscriptionID: "sub", NodeID: "old", StartedAt: startedAt},
		controllerAddress: strings.TrimPrefix(server.URL, "http://"), controllerSecret: "secret",
		runtimeCatalog: map[string]runtimeCatalogTarget{},
	}
	target := runtimeCatalogTarget{Tag: "runtime-node-002", Runtime: Runtime{TargetType: "node", SubscriptionID: "sub", NodeID: "new", NodeName: "New"}}
	runtime, err := service.hotSwitch(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if got := <-selected; got != target.Tag {
		t.Fatalf("selected outbound = %q, want %q", got, target.Tag)
	}
	if runtime.NodeID != "new" || !runtime.StartedAt.Equal(startedAt) || runtime.State != supervisor.StateRunning {
		t.Fatalf("runtime after hot switch = %+v", runtime)
	}
}

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

func TestStopCancelsRunningApplyOperation(t *testing.T) {
	t.Parallel()
	service := &Service{runtime: Runtime{State: supervisor.StateRunning}}
	started := make(chan struct{})
	applyDone := make(chan error, 1)
	go func() {
		_, err := service.runApplyOperation(context.Background(), func(ctx context.Context) (Runtime, error) {
			close(started)
			<-ctx.Done()
			return service.Status(ctx), ctx.Err()
		})
		applyDone <- err
	}()
	<-started

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	runtime, err := service.Stop(stopCtx)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.State != supervisor.StateStopped {
		t.Fatalf("runtime after stop = %+v", runtime)
	}
	select {
	case err := <-applyDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("apply error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("apply operation was not canceled by Stop")
	}
}

func TestStopPreservesAllowLanPreference(t *testing.T) {
	t.Parallel()
	service := &Service{runtime: Runtime{State: supervisor.StateRunning, AllowLan: true}}
	runtime, err := service.Stop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.State != supervisor.StateStopped || !runtime.AllowLan {
		t.Fatalf("runtime after stop = %+v, want stopped with allowLan preserved", runtime)
	}
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
