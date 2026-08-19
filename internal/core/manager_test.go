package core

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"sing-box-webui/internal/singbox"
	"sing-box-webui/internal/subscription"
)

func TestOpenInstallsEmbeddedCore(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("embedded test asset is linux/amd64")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	manager, err := Open(ctx, t.TempDir(), "")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	info, err := manager.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Source != "managed" || info.CurrentVersion != embeddedVersion || !info.UpdateSupported {
		t.Fatalf("Info() = %+v", info)
	}
	client, err := singbox.NewClient(manager.BinaryPath())
	if err != nil {
		t.Fatal(err)
	}
	output, err := client.Version(ctx)
	if err != nil || !strings.Contains(output, embeddedVersion) {
		t.Fatalf("Version() = %q, %v", output, err)
	}
	poolConfig, err := singbox.BuildPoolConfig([]subscription.Node{
		{Type: "shadowsocks", Server: "1.1.1.1", Port: 443, Method: "aes-128-gcm", Password: "one"},
		{Type: "trojan", Server: "8.8.8.8", Port: 443, Password: "two", TLS: subscription.TLS{Enabled: true, ServerName: "example.com"}},
	}, singbox.ModeSystemProxy, 2080, singbox.URLTestOptions{
		Interval: time.Minute, Tolerance: 80,
		ControllerAddress: "127.0.0.1:39092", ControllerSecret: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "pool.json")
	if err := os.WriteFile(configPath, poolConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.Check(ctx, configPath); err != nil {
		t.Fatalf("embedded sing-box rejected pool config: %v", err)
	}
}

func TestUpdateAndRollback(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("test release uses a POSIX shell executable")
	}
	manager := newTestManager(t, "1.0.0")
	archive := fakeReleaseArchive(t, "2.0.0")
	digest := sha256.Sum256(archive)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tags/v2.0.0":
			_ = json.NewEncoder(w).Encode(release{
				TagName: "v2.0.0",
				Assets: []releaseAsset{{
					Name: archiveName("2.0.0"), Digest: "sha256:" + hex.EncodeToString(digest[:]),
					Size: int64(len(archive)), URL: "http://" + r.Host + "/asset",
				}},
			})
		case "/asset":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	manager.releaseAPI = server.URL
	manager.httpClient = server.Client()

	info, err := manager.Update(context.Background(), "2.0.0")
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if info.CurrentVersion != "2.0.0" || info.PreviousVersion != "1.0.0" {
		t.Fatalf("Update() = %+v", info)
	}
	assertBinaryVersion(t, manager.BinaryPath(), "2.0.0")

	info, err = manager.Rollback(context.Background())
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if info.CurrentVersion != "1.0.0" || info.PreviousVersion != "2.0.0" {
		t.Fatalf("Rollback() = %+v", info)
	}
	assertBinaryVersion(t, manager.BinaryPath(), "1.0.0")
}

func TestUpdateRejectsDigestMismatchWithoutSwitching(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("test release uses a POSIX shell executable")
	}
	manager := newTestManager(t, "1.0.0")
	archive := fakeReleaseArchive(t, "2.0.0")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tags/v2.0.0" {
			_ = json.NewEncoder(w).Encode(release{
				TagName: "v2.0.0",
				Assets: []releaseAsset{{
					Name: archiveName("2.0.0"), Digest: "sha256:" + strings.Repeat("0", 64),
					Size: int64(len(archive)), URL: "http://" + r.Host + "/asset",
				}},
			})
			return
		}
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	manager.releaseAPI = server.URL
	manager.httpClient = server.Client()

	if _, err := manager.Update(context.Background(), "2.0.0"); err == nil {
		t.Fatal("Update() succeeded with a mismatched digest")
	}
	info, _ := manager.Info(context.Background())
	if info.CurrentVersion != "1.0.0" || info.PreviousVersion != "" {
		t.Fatalf("Info() changed after failed update: %+v", info)
	}
	assertBinaryVersion(t, manager.BinaryPath(), "1.0.0")
}

func TestExtractReleaseArchiveRejectsUncompressedSizeLimit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("archive layout is platform-specific")
	}
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{
		Name: archiveRoot("2.0.0") + "/ignored", Typeflag: tar.TypeReg,
		Size: maxExtractedSize + 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "release.tar.gz")
	if err := os.WriteFile(archivePath, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractReleaseArchive(archivePath, t.TempDir(), "2.0.0"); err == nil || !strings.Contains(err.Error(), "uncompressed content") {
		t.Fatalf("extractReleaseArchive() error = %v, want size limit error", err)
	}
}

func newTestManager(t *testing.T, version string) *Manager {
	t.Helper()
	root := filepath.Join(t.TempDir(), "core")
	if err := os.MkdirAll(filepath.Join(root, "versions"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		root: root, binaryPath: filepath.Join(root, "sing-box"), source: "managed",
		httpClient: &http.Client{Timeout: 5 * time.Second}, releaseAPI: defaultReleaseAPI,
	}
	archive := fakeReleaseArchive(t, version)
	digest := sha256.Sum256(archive)
	if err := manager.installArchive(context.Background(), bytes.NewReader(archive), version, hex.EncodeToString(digest[:])); err != nil {
		t.Fatal(err)
	}
	if err := manager.activateLocked(state{Current: version}); err != nil {
		t.Fatal(err)
	}
	return manager
}

func fakeReleaseArchive(t *testing.T, version string) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	files := map[string]struct {
		mode int64
		body string
	}{
		"sing-box": {mode: 0o700, body: fmt.Sprintf("#!/bin/sh\nprintf 'sing-box version %s\\n'\n", version)},
		"LICENSE":  {mode: 0o600, body: "test fixture\n"},
	}
	for name, file := range files {
		body := []byte(file.body)
		header := &tar.Header{
			Name: archiveRoot(version) + "/" + name, Mode: file.mode, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func assertBinaryVersion(t *testing.T, binary, version string) {
	t.Helper()
	client, err := singbox.NewClient(binary)
	if err != nil {
		t.Fatal(err)
	}
	output, err := client.Version(context.Background())
	if err != nil || !strings.Contains(output, version) {
		t.Fatalf("Version() = %q, %v; want %s", output, err, version)
	}
}
