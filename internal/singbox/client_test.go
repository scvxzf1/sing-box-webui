package singbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClientWithFakeSingBox(t *testing.T) {
	t.Parallel()

	binary := buildFakeSingBox(t)
	client, err := NewClient(binary)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	version, err := client.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if !strings.Contains(version, "1.99.0-test") {
		t.Fatalf("Version() = %q, want fake version", version)
	}

	valid := filepath.Join(t.TempDir(), "valid.json")
	if err := os.WriteFile(valid, []byte(`{"valid":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.Check(ctx, valid); err != nil {
		t.Fatalf("Check(valid) error = %v", err)
	}

	invalid := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalid, []byte(`{"invalid":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.Check(ctx, invalid); err == nil {
		t.Fatal("Check(invalid) returned nil")
	}
}

func TestSanitizeCommandOutput(t *testing.T) {
	t.Parallel()

	detail := sanitizeCommandOutput(`error: {"password":"secret-value","uuid":"11111111-1111-1111-1111-111111111111"}`)
	if strings.Contains(detail, "secret-value") || strings.Contains(detail, "11111111-1111-1111-1111-111111111111") {
		t.Fatalf("sensitive command output was not redacted: %q", detail)
	}
	if !strings.Contains(detail, "<redacted>") {
		t.Fatalf("sanitized output = %q, want redaction marker", detail)
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
