package poolhealth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerSelectsHealthyTargetAndQuarantinesFailedTarget(t *testing.T) {
	var mu sync.Mutex
	selected := ""
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-secret" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/proxies":
			_, _ = response.Write([]byte(`{"proxies":{}}`))
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "pool-member-000"):
			response.WriteHeader(http.StatusServiceUnavailable)
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "pool-member-001"):
			_ = json.NewEncoder(response).Encode(map[string]int{"delay": 42})
		case request.Method == http.MethodPut && request.URL.Path == "/proxies/runtime-pool-000":
			var input map[string]string
			_ = json.NewDecoder(request.Body).Decode(&input)
			mu.Lock()
			selected = input["name"]
			mu.Unlock()
			response.WriteHeader(http.StatusNoContent)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	manager := NewManager()
	t.Cleanup(manager.Stop)
	err := manager.Start(Config{
		Address: strings.TrimPrefix(server.URL, "http://"), Secret: "test-secret", SelectorTag: "runtime-pool-000",
		ProbeURLs: []string{"https://example.com/generate_204"}, Interval: time.Hour, Tolerance: 80 * time.Millisecond, IdleTimeout: 2 * time.Hour,
		HighLatencyThreshold: time.Second, ConsecutiveFailures: 1, RecoverySuccesses: 2, MaxBackoff: time.Minute,
		Targets: []Target{
			{Tag: "pool-member-000", NodeID: "node-a", Name: "A"},
			{Tag: "pool-member-001", NodeID: "node-b", Name: "B"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return selected == "pool-member-001"
	})
	snapshot := manager.Snapshot()
	if snapshot.State != StatusHealthy || snapshot.SelectedNodeID != "node-b" || snapshot.HealthyCount != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.Members[0].Status != StatusQuarantined && snapshot.Members[1].Status != StatusQuarantined {
		t.Fatalf("failed target was not quarantined: %+v", snapshot.Members)
	}
}

func TestManagerFailsClosedWhenEveryTargetFails(t *testing.T) {
	selected := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/proxies":
			_, _ = response.Write([]byte(`{"proxies":{}}`))
		case request.Method == http.MethodPut:
			var input map[string]string
			_ = json.NewDecoder(request.Body).Decode(&input)
			selected <- input["name"]
			response.WriteHeader(http.StatusNoContent)
		default:
			response.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	manager := NewManager()
	t.Cleanup(manager.Stop)
	if err := manager.Start(Config{
		Address: strings.TrimPrefix(server.URL, "http://"), Secret: "secret", ProbeURLs: []string{"https://example.com"},
		Interval: time.Hour, Tolerance: 80 * time.Millisecond, IdleTimeout: 2 * time.Hour,
		HighLatencyThreshold: time.Second, ConsecutiveFailures: 1, RecoverySuccesses: 1, MaxBackoff: time.Minute,
		Targets: []Target{{Tag: "pool-member-000", NodeID: "a"}, {Tag: "pool-member-001", NodeID: "b"}},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-selected:
		if value != "block" {
			t.Fatalf("selected = %q, want block", value)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for fail-closed selection")
	}
	waitFor(t, func() bool { return manager.Snapshot().State == StatusOutage })
}

func TestBestSelectionKeepsCurrentTargetWithinTolerance(t *testing.T) {
	manager := NewManager()
	manager.config.Tolerance = 80 * time.Millisecond
	manager.selected = "current"
	manager.members = map[string]*memberState{
		"current":   {target: Target{Tag: "current"}, status: StatusHealthy, latencyMS: 100},
		"candidate": {target: Target{Tag: "candidate"}, status: StatusHealthy, latencyMS: 30},
	}
	if selected := manager.bestSelectionLocked(); selected != "current" {
		t.Fatalf("selection = %q, want current within tolerance", selected)
	}
	manager.members["candidate"].latencyMS = 19
	if selected := manager.bestSelectionLocked(); selected != "candidate" {
		t.Fatalf("selection = %q, want candidate beyond tolerance", selected)
	}
}

func TestBestSelectionImmediatelyReplacesDegradedWithHealthy(t *testing.T) {
	manager := NewManager()
	manager.config.Tolerance = time.Second
	manager.selected = "current"
	manager.members = map[string]*memberState{
		"current":   {target: Target{Tag: "current"}, status: StatusDegraded, latencyMS: 10},
		"candidate": {target: Target{Tag: "candidate"}, status: StatusHealthy, latencyMS: 900},
	}
	if selected := manager.bestSelectionLocked(); selected != "candidate" {
		t.Fatalf("selection = %q, want healthy candidate", selected)
	}
}

func TestBestSelectionNeverPrefersMissingLatency(t *testing.T) {
	manager := NewManager()
	manager.members = map[string]*memberState{
		"unknown": {target: Target{Tag: "unknown"}, status: StatusHealthy},
		"known":   {target: Target{Tag: "known"}, status: StatusHealthy, latencyMS: 50},
	}
	if selected := manager.bestSelectionLocked(); selected != "known" {
		t.Fatalf("selection = %q, want measured target", selected)
	}
}

func TestBestSelectionBlocksMeasuredTargetsWithNoPassedTests(t *testing.T) {
	manager := NewManager()
	manager.members = map[string]*memberState{
		"a": {target: Target{Tag: "a"}, status: StatusDegraded, totalTests: 3},
		"b": {target: Target{Tag: "b"}, status: StatusDegraded, totalTests: 3},
	}
	if selected := manager.bestSelectionLocked(); selected != "block" {
		t.Fatalf("selection = %q, want fail-closed block", selected)
	}
}

func TestInitialSelectionPrefersMorePassedQuickTests(t *testing.T) {
	var mu sync.Mutex
	selected := ""
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/proxies":
			_, _ = response.Write([]byte(`{"proxies":{}}`))
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/delay"):
			targetURL := request.URL.Query().Get("url")
			isFirst := strings.Contains(request.URL.Path, "pool-member-000")
			if (isFirst && strings.HasSuffix(targetURL, "/three")) || (!isFirst && !strings.HasSuffix(targetURL, "/one")) {
				response.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			delay := 10
			if isFirst {
				delay = 500
			}
			_ = json.NewEncoder(response).Encode(map[string]int{"delay": delay})
		case request.Method == http.MethodPut:
			var input map[string]string
			_ = json.NewDecoder(request.Body).Decode(&input)
			mu.Lock()
			selected = input["name"]
			mu.Unlock()
			response.WriteHeader(http.StatusNoContent)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	manager := NewManager()
	t.Cleanup(manager.Stop)
	err := manager.Start(Config{
		Address: strings.TrimPrefix(server.URL, "http://"), Secret: "secret",
		ProbeURLs: []string{"https://quick.test/one", "https://quick.test/two", "https://quick.test/three"},
		Interval:  time.Hour, Tolerance: time.Second, IdleTimeout: 2 * time.Hour,
		HighLatencyThreshold: time.Second, ConsecutiveFailures: 1, RecoverySuccesses: 1, MaxBackoff: time.Minute,
		Targets: []Target{
			{Tag: "pool-member-000", NodeID: "more", Name: "More passes"},
			{Tag: "pool-member-001", NodeID: "faster", Name: "Faster but fewer"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	gotSelection := selected
	mu.Unlock()
	if gotSelection != "pool-member-000" {
		t.Fatalf("initial selection = %q, want node with more passed tests", gotSelection)
	}
	snapshot := manager.Snapshot()
	if snapshot.SelectedNodeID != "more" {
		t.Fatalf("snapshot selected node = %q, want more", snapshot.SelectedNodeID)
	}
	counts := map[string][2]int{}
	for _, member := range snapshot.Members {
		counts[member.NodeID] = [2]int{member.PassedTests, member.TotalTests}
	}
	if counts["more"] != [2]int{2, 3} || counts["faster"] != [2]int{1, 3} {
		t.Fatalf("unexpected quick-test counts: %+v", counts)
	}
}

func TestManagerPausesWhileIdleAndResumesOnTraffic(t *testing.T) {
	var traffic atomic.Int64
	var probeCount atomic.Int64
	var selectionMu sync.Mutex
	selections := make([]string, 0, 4)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/proxies":
			_, _ = response.Write([]byte(`{"proxies":{}}`))
		case request.URL.Path == "/connections":
			_ = json.NewEncoder(response).Encode(map[string]any{"uploadTotal": traffic.Load(), "downloadTotal": 0, "connections": []any{}})
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/delay"):
			probeCount.Add(1)
			_ = json.NewEncoder(response).Encode(map[string]int{"delay": 40})
		case request.Method == http.MethodPut:
			var input map[string]string
			_ = json.NewDecoder(request.Body).Decode(&input)
			selectionMu.Lock()
			selections = append(selections, input["name"])
			selectionMu.Unlock()
			response.WriteHeader(http.StatusNoContent)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	manager := NewManager()
	t.Cleanup(manager.Stop)
	if err := manager.Start(Config{
		Address: strings.TrimPrefix(server.URL, "http://"), Secret: "secret", ProbeURLs: []string{"https://example.com"},
		Interval: 20 * time.Millisecond, Tolerance: 10 * time.Millisecond, IdleTimeout: 70 * time.Millisecond,
		HighLatencyThreshold: time.Second, ConsecutiveFailures: 2, RecoverySuccesses: 2, MaxBackoff: time.Minute,
		Targets: []Target{{Tag: "pool-member-000", NodeID: "a"}, {Tag: "pool-member-001", NodeID: "b"}},
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return manager.Snapshot().Idle })
	waitFor(t, func() bool {
		selectionMu.Lock()
		defer selectionMu.Unlock()
		return len(selections) > 0 && selections[len(selections)-1] == "auto"
	})
	pausedCount := probeCount.Load()
	time.Sleep(80 * time.Millisecond)
	if current := probeCount.Load(); current != pausedCount {
		t.Fatalf("probe count changed while idle: %d -> %d", pausedCount, current)
	}
	traffic.Store(1)
	waitFor(t, func() bool { return probeCount.Load() > pausedCount && !manager.Snapshot().Idle })
}

func TestDormantSafetyProbeRunsAndKeepsAutoSelection(t *testing.T) {
	var probeCount atomic.Int64
	selected := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/delay"):
			probeCount.Add(1)
			_ = json.NewEncoder(response).Encode(map[string]int{"delay": 40})
		case request.Method == http.MethodPut:
			var input map[string]string
			_ = json.NewDecoder(request.Body).Decode(&input)
			selected <- input["name"]
			response.WriteHeader(http.StatusNoContent)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	manager := NewManager()
	now := time.Now().UTC()
	manager.config = Config{
		ProbeURLs: []string{"https://example.com"}, Interval: time.Minute,
		HighLatencyThreshold: time.Second, ConsecutiveFailures: 1, RecoverySuccesses: 1, MaxBackoff: time.Minute,
	}
	manager.client = newAPIClient(strings.TrimPrefix(server.URL, "http://"), "secret")
	manager.idle = true
	manager.dormantProbeAt = now.Add(-time.Second)
	manager.members = map[string]*memberState{
		"pool-member-000": {target: Target{Tag: "pool-member-000", NodeID: "a"}, status: StatusHealthy},
		"pool-member-001": {target: Target{Tag: "pool-member-001", NodeID: "b"}, status: StatusHealthy},
	}

	manager.checkDormantDue(t.Context())
	if probeCount.Load() != 2 {
		t.Fatalf("probe count = %d, want 2", probeCount.Load())
	}
	select {
	case value := <-selected:
		if value != "auto" {
			t.Fatalf("selection = %q, want auto while dormant", value)
		}
	default:
		t.Fatal("dormant safety probe did not update selector")
	}
	if !manager.dormantProbeAt.After(now) {
		t.Fatal("dormant safety probe was not rescheduled")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met")
}
