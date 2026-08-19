package core

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"sing-box-webui/internal/singbox"
)

const (
	maxReleaseResponse = 4 << 20
	maxArchiveSize     = 128 << 20
	maxExtractedSize   = 256 << 20
	defaultReleaseAPI  = "https://api.github.com/repos/SagerNet/sing-box/releases"
)

var (
	ErrUpdateUnsupported = errors.New("core updates are unavailable for an external sing-box binary")
	versionPattern       = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

type Info struct {
	Source          string `json:"source"`
	CurrentVersion  string `json:"currentVersion"`
	PreviousVersion string `json:"previousVersion,omitempty"`
	EmbeddedVersion string `json:"embeddedVersion"`
	UpdateSupported bool   `json:"updateSupported"`
	Platform        string `json:"platform"`
}

type state struct {
	Current  string `json:"current"`
	Previous string `json:"previous,omitempty"`
}

type Manager struct {
	mu         sync.Mutex
	root       string
	binaryPath string
	source     string
	state      state
	httpClient *http.Client
	releaseAPI string
}

func Open(ctx context.Context, dataDir, externalBinary string) (*Manager, error) {
	manager := &Manager{
		httpClient: &http.Client{Timeout: 2 * time.Minute},
		releaseAPI: defaultReleaseAPI,
	}
	if externalBinary != "" {
		client, err := singbox.NewClient(externalBinary)
		if err != nil {
			return nil, err
		}
		versionOutput, err := client.Version(ctx)
		if err != nil {
			return nil, fmt.Errorf("inspect external sing-box version: %w", err)
		}
		manager.source = "external"
		manager.binaryPath = client.BinaryPath()
		manager.state.Current = parseVersion(versionOutput)
		return manager, nil
	}

	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return nil, fmt.Errorf("embedded sing-box is unavailable for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	absoluteDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve core data directory: %w", err)
	}
	manager.source = "managed"
	manager.root = filepath.Join(absoluteDataDir, "core")
	manager.binaryPath = filepath.Join(manager.root, "sing-box")
	if err := manager.bootstrap(ctx); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) BinaryPath() string {
	if m == nil {
		return ""
	}
	return m.binaryPath
}

func (m *Manager) Info(context.Context) (Info, error) {
	if m == nil {
		return Info{}, errors.New("core manager is unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.infoLocked(), nil
}

func (m *Manager) Update(ctx context.Context, requestedVersion string) (Info, error) {
	if m == nil || m.source != "managed" {
		return Info{}, ErrUpdateUnsupported
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	version := strings.TrimPrefix(strings.TrimSpace(requestedVersion), "v")
	release, err := m.fetchRelease(ctx, version)
	if err != nil {
		return Info{}, err
	}
	version = strings.TrimPrefix(release.TagName, "v")
	if !versionPattern.MatchString(version) {
		return Info{}, fmt.Errorf("release returned an invalid version")
	}
	if version == m.state.Current {
		return m.infoLocked(), nil
	}
	assetName := archiveName(version)
	asset, ok := release.asset(assetName)
	if !ok {
		return Info{}, fmt.Errorf("release does not contain %s", assetName)
	}
	digest, err := parseDigest(asset.Digest)
	if err != nil {
		return Info{}, fmt.Errorf("release asset digest: %w", err)
	}
	if asset.Size <= 0 || asset.Size > maxArchiveSize {
		return Info{}, fmt.Errorf("release asset size is outside the allowed range")
	}

	response, err := m.doRequest(ctx, asset.URL)
	if err != nil {
		return Info{}, fmt.Errorf("download sing-box release: %w", err)
	}
	defer response.Body.Close()
	if err := m.installArchive(ctx, io.LimitReader(response.Body, maxArchiveSize+1), version, digest); err != nil {
		return Info{}, err
	}
	if err := m.activateLocked(state{Current: version, Previous: m.state.Current}); err != nil {
		return Info{}, err
	}
	return m.infoLocked(), nil
}

func (m *Manager) Rollback(_ context.Context) (Info, error) {
	if m == nil || m.source != "managed" {
		return Info{}, ErrUpdateUnsupported
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Previous == "" {
		return Info{}, errors.New("no previous core version is available")
	}
	if err := m.validateInstalled(m.state.Previous); err != nil {
		return Info{}, fmt.Errorf("validate previous core version: %w", err)
	}
	if err := m.activateLocked(state{Current: m.state.Previous, Previous: m.state.Current}); err != nil {
		return Info{}, err
	}
	return m.infoLocked(), nil
}

func (m *Manager) bootstrap(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Join(m.root, "versions"), 0o700); err != nil {
		return fmt.Errorf("create core directory: %w", err)
	}
	if err := os.Chmod(m.root, 0o700); err != nil {
		return fmt.Errorf("secure core directory: %w", err)
	}
	if err := m.installArchive(ctx, bytes.NewReader(embeddedArchive), embeddedVersion, embeddedDigest); err != nil {
		return fmt.Errorf("install embedded sing-box: %w", err)
	}

	loaded, err := m.readState()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read core state: %w", err)
	}
	if loaded.Current == "" || m.validateInstalled(loaded.Current) != nil {
		loaded = state{Current: embeddedVersion}
	}
	if loaded.Previous != "" && m.validateInstalled(loaded.Previous) != nil {
		loaded.Previous = ""
	}
	if err := m.activateLocked(loaded); err != nil {
		return fmt.Errorf("activate managed sing-box: %w", err)
	}
	return nil
}

func (m *Manager) installArchive(ctx context.Context, source io.Reader, version, expectedDigest string) error {
	if !versionPattern.MatchString(version) {
		return errors.New("invalid core version")
	}
	finalDirectory := m.versionDirectory(version)
	if err := m.validateInstalled(version); err == nil {
		return nil
	}
	if _, err := os.Stat(finalDirectory); err == nil {
		return fmt.Errorf("core version %s exists but is invalid", version)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect core version: %w", err)
	}

	archiveFile, err := os.CreateTemp(m.root, ".core-archive-*")
	if err != nil {
		return fmt.Errorf("create core archive: %w", err)
	}
	archivePath := archiveFile.Name()
	defer os.Remove(archivePath)
	if err := archiveFile.Chmod(0o600); err != nil {
		archiveFile.Close()
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(archiveFile, hash), source)
	closeErr := archiveFile.Close()
	if copyErr != nil {
		return fmt.Errorf("write core archive: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close core archive: %w", closeErr)
	}
	if written > maxArchiveSize {
		return errors.New("core archive exceeds the size limit")
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); actual != expectedDigest {
		return errors.New("core archive checksum does not match the release digest")
	}

	temporaryDirectory, err := os.MkdirTemp(filepath.Join(m.root, "versions"), ".install-*")
	if err != nil {
		return fmt.Errorf("create core installation directory: %w", err)
	}
	defer os.RemoveAll(temporaryDirectory)
	if err := os.Chmod(temporaryDirectory, 0o700); err != nil {
		return err
	}
	if err := extractReleaseArchive(archivePath, temporaryDirectory, version); err != nil {
		return err
	}
	client, err := singbox.NewClient(filepath.Join(temporaryDirectory, "sing-box"))
	if err != nil {
		return fmt.Errorf("validate installed sing-box: %w", err)
	}
	versionOutput, err := client.Version(ctx)
	if err != nil {
		return fmt.Errorf("run installed sing-box: %w", err)
	}
	if !strings.Contains(versionOutput, version) {
		return fmt.Errorf("installed sing-box reported an unexpected version")
	}
	if err := os.Rename(temporaryDirectory, finalDirectory); err != nil {
		return fmt.Errorf("publish core version: %w", err)
	}
	return nil
}

func extractReleaseArchive(archivePath, destination, version string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open core archive: %w", err)
	}
	defer gzipReader.Close()

	prefix := archiveRoot(version) + "/"
	wanted := map[string]os.FileMode{"sing-box": 0o700, "LICENSE": 0o600}
	found := make(map[string]bool, len(wanted))
	var extractedBytes int64
	tape := tar.NewReader(gzipReader)
	for {
		header, err := tape.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read core archive: %w", err)
		}
		if header.Size < 0 || header.Size > maxExtractedSize-extractedBytes {
			return errors.New("core archive uncompressed content exceeds the size limit")
		}
		extractedBytes += header.Size
		if header.Typeflag != tar.TypeReg || !strings.HasPrefix(header.Name, prefix) {
			continue
		}
		name := strings.TrimPrefix(header.Name, prefix)
		mode, ok := wanted[name]
		if !ok || strings.Contains(name, "/") {
			continue
		}
		target := filepath.Join(destination, name)
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return fmt.Errorf("create core asset %s: %w", name, err)
		}
		written, copyErr := io.Copy(output, tape)
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil {
			return fmt.Errorf("extract core asset %s: %w", name, errors.Join(copyErr, closeErr))
		}
		if written != header.Size {
			return fmt.Errorf("extract core asset %s: unexpected size", name)
		}
		found[name] = true
	}
	for name := range wanted {
		if !found[name] {
			return fmt.Errorf("core archive is missing %s", name)
		}
	}
	return nil
}

func (m *Manager) activateLocked(next state) error {
	if err := m.validateInstalled(next.Current); err != nil {
		return err
	}
	old := m.state
	if err := replaceSymlink(m.binaryPath, m.versionBinary(next.Current)); err != nil {
		return fmt.Errorf("switch core executable: %w", err)
	}
	if err := m.writeState(next); err != nil {
		if old.Current != "" {
			_ = replaceSymlink(m.binaryPath, m.versionBinary(old.Current))
		} else {
			_ = os.Remove(m.binaryPath)
		}
		return fmt.Errorf("save core state: %w", err)
	}
	m.state = next
	return nil
}

func (m *Manager) validateInstalled(version string) error {
	if !versionPattern.MatchString(version) {
		return errors.New("invalid installed core version")
	}
	_, err := singbox.NewClient(m.versionBinary(version))
	return err
}

func (m *Manager) readState() (state, error) {
	data, err := os.ReadFile(filepath.Join(m.root, "state.json"))
	if err != nil {
		return state{}, err
	}
	var value state
	if err := json.Unmarshal(data, &value); err != nil {
		return state{}, err
	}
	return value, nil
}

func (m *Manager) writeState(value state) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(m.root, ".state-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, filepath.Join(m.root, "state.json")); err != nil {
		return err
	}
	return syncDirectory(m.root)
}

func syncDirectory(directory string) error {
	handle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open core directory: %w", err)
	}
	defer handle.Close()
	if err := handle.Sync(); err != nil {
		return fmt.Errorf("sync core directory: %w", err)
	}
	return nil
}

func replaceSymlink(linkPath, target string) error {
	directory := filepath.Dir(linkPath)
	temporary, err := os.CreateTemp(directory, ".core-link-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return err
	}
	defer os.Remove(temporaryPath)
	if err := os.Symlink(target, temporaryPath); err != nil {
		return err
	}
	return os.Rename(temporaryPath, linkPath)
}

type release struct {
	TagName    string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
	URL    string `json:"browser_download_url"`
}

func (r release) asset(name string) (releaseAsset, bool) {
	for _, asset := range r.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return releaseAsset{}, false
}

func (m *Manager) fetchRelease(ctx context.Context, version string) (release, error) {
	endpoint := strings.TrimRight(m.releaseAPI, "/") + "/latest"
	if version != "" {
		if !versionPattern.MatchString(version) {
			return release{}, errors.New("version must use the form 1.2.3")
		}
		endpoint = strings.TrimRight(m.releaseAPI, "/") + "/tags/v" + version
	}
	response, err := m.doRequest(ctx, endpoint)
	if err != nil {
		return release{}, fmt.Errorf("fetch sing-box release: %w", err)
	}
	defer response.Body.Close()
	var value release
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxReleaseResponse))
	if err := decoder.Decode(&value); err != nil {
		return release{}, fmt.Errorf("decode sing-box release: %w", err)
	}
	if value.Draft || value.Prerelease {
		return release{}, errors.New("refusing a draft or prerelease sing-box release")
	}
	return value, nil
}

func (m *Manager) doRequest(ctx context.Context, url string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "sing-box-webui")
	response, err := m.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("remote server returned HTTP %d", response.StatusCode)
	}
	return response, nil
}

func (m *Manager) infoLocked() Info {
	return Info{
		Source:          m.source,
		CurrentVersion:  m.state.Current,
		PreviousVersion: m.state.Previous,
		EmbeddedVersion: embeddedVersion,
		UpdateSupported: m.source == "managed",
		Platform:        runtime.GOOS + "/" + runtime.GOARCH,
	}
}

func (m *Manager) versionDirectory(version string) string {
	return filepath.Join(m.root, "versions", version)
}

func (m *Manager) versionBinary(version string) string {
	return filepath.Join(m.versionDirectory(version), "sing-box")
}

func archiveRoot(version string) string {
	return fmt.Sprintf("sing-box-%s-%s-%s", version, runtime.GOOS, runtime.GOARCH)
}

func archiveName(version string) string {
	return archiveRoot(version) + ".tar.gz"
}

func parseDigest(value string) (string, error) {
	digest := strings.TrimPrefix(value, "sha256:")
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("missing or invalid SHA-256 digest")
	}
	return digest, nil
}

func parseVersion(output string) string {
	for _, field := range strings.Fields(output) {
		candidate := strings.TrimPrefix(field, "v")
		if versionPattern.MatchString(candidate) {
			return candidate
		}
	}
	return strings.TrimSpace(output)
}
