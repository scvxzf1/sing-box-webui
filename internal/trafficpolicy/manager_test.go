package trafficpolicy

import (
	"context"
	"testing"
	"time"

	"sing-box-webui/internal/control"
	"sing-box-webui/internal/nodepool"
	"sing-box-webui/internal/poolhealth"
	"sing-box-webui/internal/singbox"
	"sing-box-webui/internal/supervisor"
)

type fakeController struct {
	runtime control.Runtime
	applied []control.ApplyInput
}

func (f *fakeController) Status(context.Context) control.Runtime { return f.runtime }
func (f *fakeController) Apply(_ context.Context, input control.ApplyInput) (control.Runtime, error) {
	f.applied = append(f.applied, input)
	f.runtime = control.Runtime{State: supervisor.StateRunning, Mode: input.Mode, TargetType: "pool", PoolID: input.PoolID, PoolHealth: &poolhealth.Snapshot{}}
	return f.runtime, nil
}

type fakePools struct{}

func (fakePools) Get(id string) (nodepool.View, error) {
	return nodepool.View{ID: id, AvailableCount: 2}, nil
}

func TestPolicySwitchesToDownloadPoolAndRestoresOriginal(t *testing.T) {
	controller := &fakeController{}
	manager, err := Open(t.TempDir(), controller, fakePools{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Update(context.Background(), Config{
		Enabled: true, DownloadPoolID: "download", TriggerRateBytesPerSecond: 64 << 10, TriggerDurationSeconds: 2,
		ReleaseRateBytesPerSecond: 1024, ReleaseDurationSeconds: 5, CooldownSeconds: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	controller.runtime = runningPool("primary", "Primary", base, 0)
	manager.tick(context.Background(), base)
	controller.runtime = runningPool("primary", "Primary", base.Add(time.Second), 128<<10)
	manager.tick(context.Background(), base.Add(time.Second))
	controller.runtime = runningPool("primary", "Primary", base.Add(3*time.Second), 384<<10)
	manager.tick(context.Background(), base.Add(3*time.Second))
	if len(controller.applied) != 1 || controller.applied[0].PoolID != "download" {
		t.Fatalf("applied = %#v", controller.applied)
	}
	if manager.Get().State != StateActive {
		t.Fatalf("state = %s", manager.Get().State)
	}

	controller.runtime = runningPool("download", "Download", base.Add(4*time.Second), 0)
	manager.tick(context.Background(), base.Add(4*time.Second))
	controller.runtime = runningPool("download", "Download", base.Add(10*time.Second), 0)
	manager.tick(context.Background(), base.Add(10*time.Second))
	controller.runtime = runningPool("download", "Download", base.Add(16*time.Second), 0)
	manager.tick(context.Background(), base.Add(16*time.Second))
	if len(controller.applied) != 2 || controller.applied[1].PoolID != "primary" {
		t.Fatalf("applied = %#v", controller.applied)
	}
	if manager.Get().State != StateCooldown {
		t.Fatalf("state = %s", manager.Get().State)
	}
}

func TestDisablingActivePolicyRestoresOriginalPool(t *testing.T) {
	controller := &fakeController{}
	manager, err := Open(t.TempDir(), controller, fakePools{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{
		Enabled: true, DownloadPoolID: "download", TriggerRateBytesPerSecond: 64 << 10, TriggerDurationSeconds: 2,
		ReleaseRateBytesPerSecond: 1024, ReleaseDurationSeconds: 5, CooldownSeconds: 10,
	}
	if _, err := manager.Update(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	controller.runtime = runningPool("primary", "Primary", base, 0)
	manager.tick(context.Background(), base)
	controller.runtime = runningPool("primary", "Primary", base.Add(time.Second), 128<<10)
	manager.tick(context.Background(), base.Add(time.Second))
	controller.runtime = runningPool("primary", "Primary", base.Add(3*time.Second), 384<<10)
	manager.tick(context.Background(), base.Add(3*time.Second))

	config.Enabled = false
	if _, err := manager.Update(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if len(controller.applied) != 2 || controller.applied[1].PoolID != "primary" {
		t.Fatalf("applied = %#v", controller.applied)
	}
	if manager.Get().State != StateDisabled {
		t.Fatalf("state = %s", manager.Get().State)
	}
}

func runningPool(id, name string, at time.Time, download int64) control.Runtime {
	return control.Runtime{
		State: supervisor.StateRunning, Mode: singbox.ModeSystemProxy, TargetType: "pool", PoolID: id, PoolName: name,
		PoolHealth: &poolhealth.Snapshot{DownloadTotal: download, TrafficAt: at, Connections: 1},
	}
}
