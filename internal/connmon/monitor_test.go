package connmon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func clashServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (address, secret string, closeFn func()) {
	t.Helper()
	secret = "test-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler(w, r)
	}))
	return strings.TrimPrefix(server.URL, "http://"), secret, server.Close
}

func connection(id, host string, upload, download int64, chains ...string) map[string]any {
	return map[string]any{
		"id":       id,
		"upload":   upload,
		"download": download,
		"start":    time.Now().UTC().Format(time.RFC3339Nano),
		"chains":   chains,
		"metadata": map[string]any{
			"network":         "tcp",
			"type":            "http",
			"host":            host,
			"destinationIP":   "",
			"destinationPort": "443",
		},
	}
}

func TestMonitorTracksConnectionsAndResolvesNode(t *testing.T) {
	connections := []map[string]any{
		connection("a", "example.com", 100, 1000, "proxy", "auto", "pool-member-001"),
		connection("b", "example.org", 50, 500, "proxy", "pool-member-002"),
	}
	address, secret, closeFn := clashServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uploadTotal": 150, "downloadTotal": 1500, "connections": connections,
		})
	})
	defer closeFn()

	names := map[string]string{"pool-member-001": "Node A", "pool-member-002": "Node B"}
	monitor := New(nil)
	monitor.Start(address, secret, func(chains []string) string {
		for _, c := range chains {
			if n, ok := names[c]; ok {
				return n
			}
		}
		return ""
	})
	defer monitor.Stop()

	deadline := time.Now().Add(3 * time.Second)
	for {
		snap := monitor.Query(Query{})
		if len(snap.Links) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected 2 links, got %d", len(snap.Links))
		}
		time.Sleep(20 * time.Millisecond)
	}

	snap := monitor.Query(Query{})
	if !snap.Running {
		t.Fatal("expected monitor to be running")
	}
	byID := map[string]Link{}
	for _, link := range snap.Links {
		byID[link.ID] = link
	}
	if got := byID["a"].Node; got != "Node A" {
		t.Errorf("connection a node = %q, want Node A", got)
	}
	if got := byID["b"].Node; got != "Node B" {
		t.Errorf("connection b node = %q, want Node B", got)
	}
	if got := byID["a"].Host; got != "example.com:443" {
		t.Errorf("unexpected host render %q", got)
	}
	if got := byID["a"].URL; got != "example.com" {
		t.Errorf("unexpected URL render %q", got)
	}
	if snap.Stats.Active != 2 {
		t.Errorf("active = %d, want 2", snap.Stats.Active)
	}
}

func TestMonitorUsesDestinationIPWhenHostIsUnavailable(t *testing.T) {
	ipOnly := connection("ip-only", "", 1, 1, "proxy")
	metadata := ipOnly["metadata"].(map[string]any)
	metadata["destinationIP"] = "203.0.113.7"

	address, secret, closeFn := clashServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uploadTotal": 1, "downloadTotal": 1, "connections": []map[string]any{ipOnly},
		})
	})
	defer closeFn()

	monitor := New(nil)
	monitor.Start(address, secret, nil)
	defer monitor.Stop()

	waitFor(t, func() bool { return len(monitor.Query(Query{}).Links) == 1 })
	link := monitor.Query(Query{}).Links[0]
	if link.Host != "203.0.113.7:443" {
		t.Errorf("unexpected IP fallback host %q", link.Host)
	}
	if link.URL != "" {
		t.Errorf("IP fallback should not invent a URL, got %q", link.URL)
	}
}

func TestMonitorMarksClosedConnections(t *testing.T) {
	var showBoth atomic.Bool
	showBoth.Store(true)
	address, secret, closeFn := clashServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		connections := []map[string]any{connection("a", "a.com", 1, 1, "proxy", "pool-member-001")}
		if showBoth.Load() {
			connections = append(connections, connection("b", "b.com", 1, 1, "proxy", "pool-member-001"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"uploadTotal": 2, "downloadTotal": 2, "connections": connections})
	})
	defer closeFn()

	monitor := New(nil)
	monitor.Start(address, secret, func(chains []string) string { return "N" })
	defer monitor.Stop()

	waitFor(t, func() bool { return len(monitor.Query(Query{}).Links) == 2 })
	showBoth.Store(false)
	waitFor(t, func() bool {
		snap := monitor.Query(Query{})
		for _, l := range snap.Links {
			if l.Host != "" && !l.Active {
				return true
			}
		}
		return false
	})
	snap := monitor.Query(Query{})
	if snap.Stats.Active != 1 {
		t.Errorf("active = %d, want 1", snap.Stats.Active)
	}
}

func TestMonitorEvictsAtCapacity(t *testing.T) {
	connections := make([]map[string]any, 0, MaxLinks+50)
	for i := 0; i < MaxLinks+50; i++ {
		connections = append(connections, connection(fmt.Sprintf("id-%d", i), fmt.Sprintf("h%d.com", i), 1, 1, "proxy", "pool-member-001"))
	}
	address, secret, closeFn := clashServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"uploadTotal": 1, "downloadTotal": 1, "connections": connections})
	})
	defer closeFn()

	monitor := New(nil)
	monitor.Start(address, secret, func(chains []string) string { return "N" })
	defer monitor.Stop()

	waitFor(t, func() bool { return len(monitor.Query(Query{}).Links) >= MaxLinks })
	snap := monitor.Query(Query{})
	if len(snap.Links) > MaxLinks {
		t.Fatalf("cache exceeded capacity: %d", len(snap.Links))
	}
}

func TestQuerySearchAndSort(t *testing.T) {
	monitor := New(nil)
	monitor.links = map[string]*Link{
		"1": {ID: "1", Host: "beta.com", URL: "https://beta.com", Node: "NodeA", Download: 10, Active: true},
		"2": {ID: "2", Host: "alpha.com", Node: "NodeB", Download: 99, Active: true},
		"3": {ID: "3", Host: "gamma.io", Node: "NodeA", Download: 50, Active: false},
	}

	// Search by host substring.
	snap := monitor.Query(Query{Search: "alpha"})
	if len(snap.Links) != 1 || snap.Links[0].Host != "alpha.com" {
		t.Fatalf("search by host failed: %+v", snap.Links)
	}

	// Search by reported URL/domain.
	snap = monitor.Query(Query{Search: "https://beta.com"})
	if len(snap.Links) != 1 || snap.Links[0].URL != "https://beta.com" {
		t.Fatalf("search by URL failed: %+v", snap.Links)
	}

	// Search by node name.
	snap = monitor.Query(Query{Search: "nodea"})
	if len(snap.Links) != 2 {
		t.Fatalf("search by node failed: %+v", snap.Links)
	}

	// Multi-key sort: node asc, then download desc.
	desc := true
	snap = monitor.Query(Query{Sort: []Ordering{
		{Key: SortNode, Desc: false},
		{Key: SortDownload, Desc: desc},
	}})
	if len(snap.Links) != 3 {
		t.Fatalf("expected 3 links, got %d", len(snap.Links))
	}
	// Active first (invariant), then NodeA sorted by download desc → gamma(50) is inactive so it goes last.
	// Active group: beta.com(NodeA,10), alpha.com(NodeB,99); inactive: gamma.io(NodeA,50).
	gotOrder := []string{snap.Links[0].Host, snap.Links[1].Host, snap.Links[2].Host}
	if gotOrder[2] != "gamma.io" {
		t.Errorf("inactive link should sort last regardless of sort keys, got %v", gotOrder)
	}
	if snap.Links[0].Node != "NodeA" {
		t.Errorf("first active should be NodeA, got %v", snap.Links[0].Node)
	}
}

func TestMonitorResolvesNodeThroughGroupSelection(t *testing.T) {
	address, secret, closeFn := clashServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			w.WriteHeader(http.StatusNoContent)
		case "/proxies":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"proxies": map[string]any{
					"proxy":           map[string]any{"type": "Selector", "now": "auto"},
					"auto":            map[string]any{"type": "URLTest", "now": "pool-member-001"},
					"pool-member-001": map[string]any{"type": "Trojan"},
				},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"uploadTotal": 1, "downloadTotal": 1,
				"connections": []map[string]any{connection("a", "example.com", 1, 1, "auto", "proxy")},
			})
		}
	})
	defer closeFn()

	names := map[string]string{"pool-member-001": "Node B"}
	monitor := New(nil)
	monitor.Start(address, secret, func(chains []string) string {
		for _, c := range chains {
			if n, ok := names[c]; ok {
				return n
			}
		}
		return ""
	})
	defer monitor.Stop()

	waitFor(t, func() bool {
		snap := monitor.Query(Query{})
		return len(snap.Links) == 1 && snap.Links[0].Node == "Node B"
	})
}

func TestResetClearsCache(t *testing.T) {
	monitor := New(nil)
	monitor.links["1"] = &Link{ID: "1", Host: "x.com", Active: true}
	monitor.order = []string{"1"}
	monitor.Reset()
	snap := monitor.Query(Query{})
	if len(snap.Links) != 0 {
		t.Fatalf("expected empty cache after reset, got %d", len(snap.Links))
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}
