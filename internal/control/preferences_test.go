package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"sing-box-webui/internal/supervisor"
)

func TestRuntimePreferencesPersistAndRestoreAllowLan(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "runtime", "preferences.json")
	if err := saveRuntimePreferences(path, runtimePreferences{AllowLan: true}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("preferences permissions = %o, want 600", got)
	}

	service, err := New(Config{PreferencesPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if !service.runtime.AllowLan {
		t.Fatal("restored runtime did not preserve allowLan")
	}
}

func TestSetAllowLanPersistsImmediately(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "preferences.json")
	service, err := New(Config{PreferencesPath: path})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := service.SetAllowLan(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.AllowLan {
		t.Fatal("SetAllowLan() did not update stopped runtime")
	}
	restored, err := New(Config{PreferencesPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if !restored.runtime.AllowLan {
		t.Fatal("SetAllowLan() preference was not restored")
	}

	service.runtime.State = supervisor.StateRunning
	if _, err := service.SetAllowLan(context.Background(), false); !errors.Is(err, ErrRuntimeBusy) {
		t.Fatalf("SetAllowLan() error = %v, want ErrRuntimeBusy", err)
	}
	preferences, err := loadRuntimePreferences(path)
	if err != nil {
		t.Fatal(err)
	}
	if !preferences.AllowLan {
		t.Fatal("failed running update changed persisted preference")
	}
}

func TestRuntimePreferencesRejectMalformedFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := os.WriteFile(path, []byte(`{"allowLan":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{PreferencesPath: path}); err == nil {
		t.Fatal("New() accepted malformed runtime preferences")
	}
}
