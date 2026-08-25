package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sing-box-webui/internal/control"
	"sing-box-webui/internal/dnsprofile"
	"sing-box-webui/internal/events"
	"sing-box-webui/internal/latency"
	"sing-box-webui/internal/proxychannel"
	"sing-box-webui/internal/routing"
	"sing-box-webui/internal/subscription"
)

type fakeLatencyTester struct {
	response latency.Response
	err      error
	ids      []string
}

func (tester *fakeLatencyTester) Test(_ context.Context, _ string, ids []string) (latency.Response, error) {
	tester.ids = append([]string(nil), ids...)
	return tester.response, tester.err
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	server, err := NewServer(ServerConfig{
		Address:              "127.0.0.1:11872",
		DevOrigin:            "http://127.0.0.1:5173",
		Version:              "test",
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
		Events:               events.NewBroker(4, 2),
		AllowUnauthenticated: true,
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}

func TestHealth(t *testing.T) {
	t.Parallel()
	server := newTestServer(t)

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:11872/healthz", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestNewServerRequiresExplicitAuthenticationMode(t *testing.T) {
	t.Parallel()
	if _, err := NewServer(ServerConfig{Address: "127.0.0.1:11872"}); err == nil {
		t.Fatal("NewServer() accepted an empty web token without an explicit unauthenticated mode")
	}
}

func TestWebAuthentication(t *testing.T) {
	t.Parallel()
	const webToken = "test-token-with-32-characters-value"
	server, err := NewServer(ServerConfig{
		Address: "127.0.0.1:11872", DevOrigin: "http://127.0.0.1:5173", WebToken: webToken,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:11872/api/v1/session", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated session status = %d, want 401", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:11872/api/v1/auth/login", strings.NewReader(`{"token":"wrong-token-value"}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid login status = %d, want 401", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:11872/api/v1/auth/login", strings.NewReader(`{"token":"test-token-with-32-characters-value","extra":true}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("login with unknown field status = %d, want 400", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:11872/api/v1/auth/login", strings.NewReader(`{"token":"test-token-with-32-characters-value"}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(response.Result().Cookies()) != 1 {
		t.Fatalf("login status = %d cookies=%d body=%s", response.Code, len(response.Result().Cookies()), response.Body.String())
	}
	if strings.Contains(response.Body.String(), webToken) || strings.Contains(response.Header().Get("Set-Cookie"), webToken) {
		t.Fatal("successful login response disclosed the Web token")
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("login Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
	cookie := response.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie flags: HttpOnly=%v SameSite=%v", cookie.HttpOnly, cookie.SameSite)
	}

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:11872/api/v1/session", nil)
	request.AddCookie(cookie)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated session status = %d, want 200", response.Code)
	}
	if strings.Contains(response.Body.String(), webToken) {
		t.Fatal("session response disclosed the Web token")
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("session Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
}

func TestRejectsUnexpectedHostAndOrigin(t *testing.T) {
	t.Parallel()
	server := newTestServer(t)

	tests := []struct {
		name   string
		host   string
		origin string
		code   int
	}{
		{name: "unexpected host", host: "evil.example", code: http.StatusBadRequest},
		{name: "cross origin", host: "127.0.0.1:11872", origin: "https://evil.example", code: http.StatusForbidden},
		{name: "vite origin", host: "127.0.0.1:11872", origin: "http://127.0.0.1:5173", code: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:11872/healthz", nil)
			request.Host = test.host
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != test.code {
				t.Fatalf("status = %d, want %d", response.Code, test.code)
			}
		})
	}
}

func TestUnsafeRequestsRequireOriginAndCSRFToken(t *testing.T) {
	t.Parallel()
	server := newTestServer(t)

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:11872/api/v1/runtime/stop", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("request without origin status = %d, want 403", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:11872/api/v1/runtime/stop", nil)
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("request without CSRF status = %d, want 403", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:11872/api/v1/runtime/stop", nil)
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("authorized request status = %d, want 503 from unavailable runtime", response.Code)
	}
}

func TestNotFoundUsesErrorEnvelope(t *testing.T) {
	t.Parallel()
	server := newTestServer(t)

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:11872/api/v1/missing", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	var body errorBody
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != http.StatusNotFound || body.Error.Code != "not_found" || body.RequestID == "" {
		t.Fatalf("unexpected error response: status=%d body=%+v", response.Code, body)
	}
}

func TestNodeLatencyEndpoint(t *testing.T) {
	t.Parallel()
	tester := &fakeLatencyTester{
		response: latency.Response{
			Items: []latency.Result{{NodeID: "node-1", Name: "Node", Status: latency.StatusOK}},
		},
	}
	server, err := NewServer(ServerConfig{
		Address: "127.0.0.1:11872", DevOrigin: "http://127.0.0.1:5173", Latency: tester, AllowUnauthenticated: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:11872/api/v1/subscriptions/sub-1/latency", strings.NewReader(`{"nodeIds":["node-1"]}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(tester.ids) != 1 || tester.ids[0] != "node-1" {
		t.Fatalf("status=%d ids=%v body=%s", response.Code, tester.ids, response.Body.String())
	}

	tester.err = latency.ErrUnavailable
	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:11872/api/v1/subscriptions/sub-1/latency", strings.NewReader(`{"nodeIds":["node-1"]}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var errorResponse errorBody
	if err := json.NewDecoder(response.Body).Decode(&errorResponse); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusServiceUnavailable || errorResponse.Error.Code != "latency_unavailable" {
		t.Fatalf("status=%d body=%+v", response.Code, errorResponse)
	}
}

func TestRuntimePreferencesEndpointPersistsAllowLan(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "runtime", "preferences.json")
	controlService, err := control.New(control.Config{PreferencesPath: path})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{
		Address: "127.0.0.1:11872", DevOrigin: "http://127.0.0.1:5173", Control: controlService, AllowUnauthenticated: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPut, "http://127.0.0.1:11872/api/v1/runtime/preferences", strings.NewReader(`{"allowLan":true}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var runtime control.Runtime
	if err := json.Unmarshal(response.Body.Bytes(), &runtime); err != nil {
		t.Fatal(err)
	}
	if runtime.State != "stopped" || !runtime.AllowLan {
		t.Fatalf("runtime = %+v", runtime)
	}
	restored, err := control.New(control.Config{PreferencesPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Status(context.Background()).AllowLan {
		t.Fatal("persisted LAN preference was not restored")
	}

	request = httptest.NewRequest(http.MethodPut, "http://127.0.0.1:11872/api/v1/runtime/preferences", strings.NewReader(`{}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing allowLan status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestImportSubscriptionNodesReturnsSanitizedPartialResults(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	stored, err := json.Marshal([]subscription.Subscription{{
		ID: "sub-1", Name: "Main", URL: "https://subscription.example.com", Active: true, Nodes: []subscription.Node{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "subscriptions.json"), stored, 0o600); err != nil {
		t.Fatal(err)
	}
	subscriptions, err := subscription.OpenManager(directory, nil)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{
		Address: "127.0.0.1:11872", DevOrigin: "http://127.0.0.1:5173", Subscriptions: subscriptions, AllowUnauthenticated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(subscription.ImportNodesInput{
		Links: "trojan://manual-secret@manual.example.com:443#Manual\nftp://hidden-secret@invalid.example.com:21",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:11872/api/v1/subscriptions/sub-1/nodes/import", strings.NewReader(string(payload)))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "manual-secret") || strings.Contains(response.Body.String(), "hidden-secret") {
		t.Fatalf("response leaked node credentials: %s", response.Body.String())
	}
	var result subscription.ImportNodesResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.AddedCount != 1 || result.InvalidCount != 1 || result.Subscription.NodeCount != 1 {
		t.Fatalf("result = %+v", result)
	}

	nodeID := result.Items[0].Node.ID
	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:11872/api/v1/subscriptions/sub-1/nodes/"+nodeID+"/link", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("node link status=%d body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var link subscription.NodeLink
	if err := json.Unmarshal(response.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	if link.Link != "trojan://manual-secret@manual.example.com:443#Manual" || link.Source != subscription.NodeLinkSourceOriginal {
		t.Fatalf("node link response = %+v", link)
	}
}

func TestProxyChannelEndpointsCreateListAndDownloadCertificate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	subscriptionDirectory := filepath.Join(root, "subscriptions")
	if err := os.MkdirAll(subscriptionDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	stored, err := json.Marshal([]subscription.Subscription{{
		ID: "sub-1", Name: "Primary", Nodes: []subscription.Node{{
			ID: "node-1", Name: "Tokyo", Type: "shadowsocks", Server: "1.1.1.1", Port: 443,
			Method: "aes-128-gcm", Password: "node-secret",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subscriptionDirectory, "subscriptions.json"), stored, 0o600); err != nil {
		t.Fatal(err)
	}
	subscriptions, err := subscription.OpenManager(subscriptionDirectory, nil)
	if err != nil {
		t.Fatal(err)
	}
	channels, err := proxychannel.OpenManager(filepath.Join(root, "channels"), subscriptions, 2080)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{
		Address: "127.0.0.1:11872", DevOrigin: "http://127.0.0.1:5173",
		Subscriptions: subscriptions, Channels: channels, AllowUnauthenticated: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:11872/api/v1/channels", strings.NewReader(`{"name":"LAN HTTP","protocol":"http","direction":"reverse","port":18080,"node":{"subscriptionId":"sub-1","nodeId":"node-1"},"enabled":true}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unauthenticated shared channel status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:11872/api/v1/channels", strings.NewReader(`{"name":"LAN HTTP","protocol":"http","direction":"reverse","port":18080,"username":"browser","password":"channel-secret","node":{"subscriptionId":"sub-1","nodeId":"node-1"},"enabled":true}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:11872/api/v1/channels", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var list struct {
		Items []proxychannel.View `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(list.Items) != 1 || list.Items[0].ListenAddress != "0.0.0.0:18080" || list.Items[0].AccessAddresses == nil || !list.Items[0].Available {
		t.Fatalf("status=%d channels=%+v", response.Code, list.Items)
	}

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:11872/api/v1/channels/certificate", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "BEGIN CERTIFICATE") || strings.Contains(response.Body.String(), "PRIVATE KEY") {
		t.Fatalf("certificate status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestRulesEndpointCreatesAndListsManualRule(t *testing.T) {
	t.Parallel()
	rules, err := routing.OpenManager(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{
		Address: "127.0.0.1:11872", DevOrigin: "http://127.0.0.1:5173", Rules: rules, AllowUnauthenticated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:11872/api/v1/rules", strings.NewReader(`{"name":"Private direct","enabled":true,"conditions":[{"type":"ip_is_private"}],"action":"direct"}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:11872/api/v1/rules", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var body struct {
		Items []routing.Rule `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(body.Items) != 2 || body.Items[0].Origin != routing.OriginManual || body.Items[1].ID != routing.BuiltinGlobalID {
		t.Fatalf("status=%d items=%#v", response.Code, body.Items)
	}
}

func TestRulePoolsEndpointAtomicallyReplacesPoolRules(t *testing.T) {
	t.Parallel()
	rules, err := routing.OpenManager(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{
		Address: "127.0.0.1:11872", DevOrigin: "http://127.0.0.1:5173", Rules: rules, AllowUnauthenticated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:11872/api/v1/rule-pools", strings.NewReader(`{"name":"Local services","enabled":true,"rules":[]}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	createdBody := response.Body.String()
	if !strings.Contains(createdBody, `"rules":[]`) {
		t.Fatalf("empty pool rules must be an array: %s", createdBody)
	}
	var pool routing.RulePool
	if err := json.NewDecoder(strings.NewReader(createdBody)).Decode(&pool); err != nil {
		t.Fatal(err)
	}

	request = httptest.NewRequest(http.MethodPatch, "http://127.0.0.1:11872/api/v1/rule-pools/"+pool.ID, strings.NewReader(`{"rules":[{"name":"Private direct","enabled":true,"conditions":[{"type":"ip_is_private"}],"action":"direct"}]}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&pool); err != nil {
		t.Fatal(err)
	}
	if len(pool.Rules) != 1 || pool.Rules[0].Name != "Private direct" {
		t.Fatalf("updated pool = %#v", pool)
	}

	request = httptest.NewRequest(http.MethodPatch, "http://127.0.0.1:11872/api/v1/rule-pools/"+pool.ID, strings.NewReader(`{"rules":[{"name":"invalid","enabled":true,"conditions":[],"action":"direct"}]}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || len(rules.ListPools()[0].Rules) != 1 {
		t.Fatalf("invalid replacement status=%d pools=%#v", response.Code, rules.ListPools())
	}
}

func TestDnsProfileEndpointRoundTrip(t *testing.T) {
	t.Parallel()
	dns, err := dnsprofile.OpenManager(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{
		Address: "127.0.0.1:11872", DevOrigin: "http://127.0.0.1:5173", DNS: dns, AllowUnauthenticated: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:11872/api/v1/dns/profile", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var profile dnsprofile.Profile
	if err := json.NewDecoder(response.Body).Decode(&profile); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(profile.Servers) != 1 || profile.Servers[0].Tag != "dns-google" || profile.Strategy != "prefer_ipv4" {
		t.Fatalf("GET status=%d profile=%+v", response.Code, profile)
	}

	request = httptest.NewRequest(http.MethodPut, "http://127.0.0.1:11872/api/v1/dns/profile", strings.NewReader(`{"servers":[{"tag":"dns-alibaba","type":"udp","server":"223.5.5.5"}],"final":"dns-alibaba","strategy":"ipv4_only","fakeIP":{"enabled":true}}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if err := json.NewDecoder(response.Body).Decode(&profile); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || profile.Final != "dns-alibaba" || !profile.FakeIP.Enabled || profile.FakeIP.Inet4Range != "198.18.0.0/15" {
		t.Fatalf("PUT status=%d profile=%+v body-check failed", response.Code, profile)
	}

	request = httptest.NewRequest(http.MethodPut, "http://127.0.0.1:11872/api/v1/dns/profile", strings.NewReader(`{"servers":[],"final":"","strategy":"prefer_ipv4","fakeIP":{"enabled":false}}`))
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-CSRF-Token", server.csrfToken)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var errorResponse errorBody
	if err := json.NewDecoder(response.Body).Decode(&errorResponse); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusUnprocessableEntity || errorResponse.Error.Code != "dns_profile_invalid" {
		t.Fatalf("invalid PUT status=%d body=%+v", response.Code, errorResponse)
	}
}

func TestEventStreamStartsWithSnapshot(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:11872/api/v1/events", nil)
	ctx, cancel := contextWithCancel(request)
	request = request.WithContext(ctx)

	reader, writer := io.Pipe()
	response := &streamRecorder{header: make(http.Header), writer: writer}
	done := make(chan struct{})
	go func() {
		server.Handler().ServeHTTP(response, request)
		_ = writer.Close()
		close(done)
	}()

	scanner := bufio.NewScanner(reader)
	foundSnapshot := false
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "event: snapshot") {
			foundSnapshot = true
			break
		}
	}
	cancel()
	<-done
	if !foundSnapshot {
		t.Fatal("event stream did not start with a snapshot")
	}
}

type streamRecorder struct {
	header http.Header
	writer io.Writer
}

func (r *streamRecorder) Header() http.Header         { return r.header }
func (r *streamRecorder) WriteHeader(_ int)           {}
func (r *streamRecorder) Write(p []byte) (int, error) { return r.writer.Write(p) }
func (r *streamRecorder) Flush()                      {}

func contextWithCancel(request *http.Request) (context.Context, context.CancelFunc) {
	return context.WithCancel(request.Context())
}
