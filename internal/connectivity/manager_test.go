package connectivity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubResolver returns a fixed proxy address, mimicking the control service.
type stubResolver struct {
	address string
	running bool
}

func (s stubResolver) ProxyAddress() string { return s.address }
func (s stubResolver) ProxyRunning() bool   { return s.running }

func openManager(t *testing.T, resolver ProxyResolver) *Manager {
	t.Helper()
	manager, err := Open(t.TempDir(), resolver)
	if err != nil {
		t.Fatal(err)
	}
	manager.allowPrivateTargets = true
	return manager
}

func TestOpenSeedsDefaultTargets(t *testing.T) {
	t.Parallel()
	manager := openManager(t, stubResolver{})
	items := manager.List()
	if len(items) != 2 {
		t.Fatalf("expected 2 seeded targets, got %d: %+v", len(items), items)
	}
	names := map[string]string{}
	for _, item := range items {
		names[item.Name] = item.URL
	}
	if names["GitHub"] != "https://github.com" || names["YouTube"] != "https://www.youtube.com/generate_204" {
		t.Fatalf("unexpected seeded targets: %+v", names)
	}
}

func TestOpenRejectsNilResolver(t *testing.T) {
	t.Parallel()
	if _, err := Open(t.TempDir(), nil); err == nil {
		t.Fatal("Open() accepted a nil resolver")
	}
}

func TestCreateNormalizesAndPersists(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	manager, err := Open(directory, stubResolver{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create(CreateInput{Name: "  Example  ", URL: " https://example.com/health "})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Name != "Example" || created.URL != "https://example.com/health" {
		t.Fatalf("unexpected created target: %+v", created)
	}

	reopened, err := Open(directory, stubResolver{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range reopened.List() {
		if item.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("created target %s not persisted across reopen", created.ID)
	}
}

func TestCreateValidation(t *testing.T) {
	t.Parallel()
	manager := openManager(t, stubResolver{})
	cases := []CreateInput{
		{Name: "", URL: "https://example.com"},
		{Name: "   ", URL: "https://example.com"},
		{Name: "Bad", URL: "not-a-url"},
		{Name: "Bad", URL: "ftp://example.com"},
		{Name: "Bad", URL: "https://"},
		{Name: strings.Repeat("长", maxNameLength+1), URL: "https://example.com"},
	}
	for index, input := range cases {
		if _, err := manager.Create(input); err == nil {
			t.Fatalf("case %d: Create(%+v) succeeded, want error", index, input)
		}
	}
}

func TestUpdateAndDelete(t *testing.T) {
	t.Parallel()
	manager := openManager(t, stubResolver{})
	created, err := manager.Create(CreateInput{Name: "Old", URL: "https://old.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	newName := "New"
	updated, err := manager.Update(created.ID, UpdateInput{Name: &newName})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "New" || updated.URL != "https://old.example.com" {
		t.Fatalf("unexpected updated target: %+v", updated)
	}
	invalid := "not-a-url"
	if _, err := manager.Update(created.ID, UpdateInput{URL: &invalid}); err == nil {
		t.Fatal("Update() accepted an invalid URL")
	}
	if _, err := manager.Update("missing", UpdateInput{Name: &newName}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update(missing) error = %v, want ErrNotFound", err)
	}

	if err := manager.Delete("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete(missing) error = %v, want ErrNotFound", err)
	}
	if err := manager.Delete(created.ID); err != nil {
		t.Fatal(err)
	}
	for _, item := range manager.List() {
		if item.ID == created.ID {
			t.Fatalf("target %s still listed after delete", created.ID)
		}
	}
}

func TestListSortedByName(t *testing.T) {
	t.Parallel()
	manager := openManager(t, stubResolver{})
	if _, err := manager.Create(CreateInput{Name: "AAA", URL: "https://a.example.com"}); err != nil {
		t.Fatal(err)
	}
	items := manager.List()
	for index := 1; index < len(items); index++ {
		if items[index-1].Name > items[index].Name {
			t.Fatalf("List() not name-sorted: %+v", items)
		}
	}
}

func TestTestDirectOnlyWhenNoProxy(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	manager := openManager(t, stubResolver{})
	target, err := manager.Create(CreateInput{Name: "Local", URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	response, err := manager.Test(context.Background(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("expected 1 result, got %d", len(response.Items))
	}
	result := response.Items[0]
	if result.Direct.Status != StatusOK || result.Direct.LatencyMS == nil {
		t.Fatalf("direct probe failed: %+v", result.Direct)
	}
	if result.Proxy != nil {
		t.Fatalf("expected no proxy result without resolver address, got %+v", result.Proxy)
	}
}

func TestTestDualPathThroughProxy(t *testing.T) {
	t.Parallel()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(backend.Close)

	// A minimal HTTP forward proxy that reports which requests it carried.
	var proxiedRequests int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxiedRequests++
		outbound, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		response, err := http.DefaultClient.Do(outbound)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		w.WriteHeader(response.StatusCode)
	}))
	t.Cleanup(proxy.Close)
	proxyAddress := strings.TrimPrefix(proxy.URL, "http://")

	manager := openManager(t, stubResolver{address: proxyAddress})
	target, err := manager.Create(CreateInput{Name: "Local", URL: backend.URL})
	if err != nil {
		t.Fatal(err)
	}
	response, err := manager.Test(context.Background(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := response.Items[0]
	if result.Direct.Status != StatusOK {
		t.Fatalf("direct probe failed: %+v", result.Direct)
	}
	if result.Proxy == nil || result.Proxy.Status != StatusOK {
		t.Fatalf("proxied probe failed: %+v", result.Proxy)
	}
	if proxiedRequests == 0 {
		t.Fatal("proxy server observed no requests")
	}
}

func TestTestUnknownIDReturnsNotFound(t *testing.T) {
	t.Parallel()
	manager := openManager(t, stubResolver{})
	if _, err := manager.Test(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Test(missing) error = %v, want ErrNotFound", err)
	}
}

func TestTestAllMeasuresEveryTarget(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	manager := openManager(t, stubResolver{})
	for index := 0; index < 3; index++ {
		if _, err := manager.Create(CreateInput{Name: fmt.Sprintf("T%d", index), URL: server.URL}); err != nil {
			t.Fatal(err)
		}
	}
	response, err := manager.Test(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	// 3 created + 2 seeded defaults (which fail to resolve offline, but are still measured).
	if len(response.Items) != 5 {
		t.Fatalf("expected 5 results, got %d", len(response.Items))
	}
	okCount := 0
	for _, item := range response.Items {
		if item.URL == server.URL && item.Direct.Status == StatusOK {
			okCount++
		}
	}
	if okCount != 3 {
		t.Fatalf("expected 3 reachable local targets, got %d: %+v", okCount, response.Items)
	}
}

func TestProbeFailsOnRefusedConnection(t *testing.T) {
	t.Parallel()
	// Bind then immediately close a listener to obtain a port that refuses connections.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()

	result := probe(context.Background(), "http://"+address+"/", "", true)
	if result.Status == StatusOK {
		t.Fatalf("expected failure probing refused connection, got %+v", result)
	}
}

func TestProbeBlocksPrivateTargets(t *testing.T) {
	t.Parallel()
	result := probe(context.Background(), "http://127.0.0.1:33334/healthz", "", false)
	if result.Status != StatusFailed || result.Detail != "目标地址不可访问" {
		t.Fatalf("private target result = %+v", result)
	}
}

func TestSummarizeErrorKeepsActionableCause(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("Get \"https://x\": %w", errors.New("connection refused"))
	if got := summarizeError(wrapped); got != "connection refused" {
		t.Fatalf("summarizeError() = %q, want %q", got, "connection refused")
	}
	plain := errors.New("plain failure")
	if got := summarizeError(plain); got != "plain failure" {
		t.Fatalf("summarizeError() = %q, want %q", got, "plain failure")
	}
}

func TestDiagnoseRequiresRunningProxyAndKnownProvider(t *testing.T) {
	t.Parallel()
	manager := openManager(t, stubResolver{})
	if _, err := manager.Diagnose(context.Background(), DiagnosticInput{Kind: "exit", Provider: "ipify"}); !errors.Is(err, ErrProxyStopped) {
		t.Fatalf("Diagnose() error = %v, want ErrProxyStopped", err)
	}
	if _, err := manager.Diagnose(context.Background(), DiagnosticInput{Kind: "quality", Provider: "ipify"}); err == nil {
		t.Fatal("Diagnose() accepted a provider from another diagnostic kind")
	}
	if _, err := manager.Diagnose(context.Background(), DiagnosticInput{Kind: "exit", Provider: "custom"}); err == nil {
		t.Fatal("Diagnose() accepted an unknown provider")
	}
}

func TestParseDiagnosticBody(t *testing.T) {
	t.Parallel()
	result := DiagnosticResult{}
	parseDiagnosticBody([]byte(`{
		"ip":"203.0.113.8","asn":64500,"asOrganization":"Example Network",
		"country":"Example","countryCode":"EX","region":"West","city":"Test",
		"fraudScore":12,"isResidential":true
	}`), &result)
	if result.IP != "203.0.113.8" || result.ASN != "64500" || result.Organization != "Example Network" {
		t.Fatalf("unexpected identity fields: %+v", result)
	}
	if result.FraudScore == nil || *result.FraudScore != 12 || result.Residential == nil || !*result.Residential {
		t.Fatalf("unexpected quality fields: %+v", result)
	}

	plain := DiagnosticResult{}
	parseDiagnosticBody([]byte("2001:db8::1\n"), &plain)
	if plain.IP != "2001:db8::1" {
		t.Fatalf("plain-text IP = %q", plain.IP)
	}
}

func TestPersistFilePermissions(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	manager, err := Open(directory, stubResolver{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(CreateInput{Name: "X", URL: "https://x.example.com"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(directory, "connectivity-targets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("persisted file mode = %o, want 600", mode)
	}
	// Round-trip: file must decode back into the same target list.
	content, err := os.ReadFile(filepath.Join(directory, "connectivity-targets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded []Target
	if err := json.Unmarshal(content, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 3 {
		t.Fatalf("expected 3 persisted targets, got %d", len(decoded))
	}
}
