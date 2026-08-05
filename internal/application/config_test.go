package application

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateLoopbackAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		address string
		wantErr bool
	}{
		{name: "default IPv4", address: "127.0.0.1:11872"},
		{name: "explicit IPv6", address: "[::1]:11872"},
		{name: "unspecified IPv4", address: "0.0.0.0:11872", wantErr: true},
		{name: "unspecified IPv6", address: "[::]:11872", wantErr: true},
		{name: "hostname", address: "localhost:11872", wantErr: true},
		{name: "missing port", address: "127.0.0.1", wantErr: true},
		{name: "invalid port", address: "127.0.0.1:70000", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateLoopbackAddress(test.address)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateLoopbackAddress(%q) error = %v, wantErr %v", test.address, err, test.wantErr)
			}
		})
	}
}

func TestLoadOrCreateWebToken(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	first, firstEnabled, err := loadOrCreateWebConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	second, secondEnabled, err := loadOrCreateWebConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !firstEnabled || !secondEnabled || len(first) < 32 || second != first {
		t.Fatalf("token was not generated and persisted: first length=%d equal=%v", len(first), second == first)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %o, want 600", got)
	}
}

func TestLoadOrCreateWebTokenRejectsShortToken(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"web":{"token":"short"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadOrCreateWebConfig(path); err == nil {
		t.Fatal("expected short token to be rejected")
	}
}

func TestLoadWebConfigCanDisableAuthentication(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"web":{"enabled":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	token, enabled, err := loadOrCreateWebConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if enabled || token != "" {
		t.Fatalf("disabled config returned enabled=%v token=%q", enabled, token)
	}
}
