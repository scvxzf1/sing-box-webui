package nodepool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"sing-box-webui/internal/subscription"
)

func TestManagerPersistsAndResolvesCrossSubscriptionMembers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	subscriptionDirectory := filepath.Join(root, "subscriptions")
	if err := os.MkdirAll(subscriptionDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	items := []subscription.Subscription{
		{ID: "sub-a", Name: "A", Nodes: []subscription.Node{{ID: "node-a", Name: "Tokyo", Type: "shadowsocks", Server: "1.1.1.1", Port: 443}}},
		{ID: "sub-b", Name: "B", Nodes: []subscription.Node{{ID: "node-b", Name: "London", Type: "trojan", Server: "8.8.8.8", Port: 443}}},
	}
	content, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subscriptionDirectory, "subscriptions.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	subscriptions, err := subscription.OpenManager(subscriptionDirectory, nil)
	if err != nil {
		t.Fatal(err)
	}
	poolDirectory := filepath.Join(root, "pools")
	manager, err := OpenManager(poolDirectory, subscriptions)
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create(CreateInput{
		Name: "Primary", Members: []Member{
			{SubscriptionID: "sub-a", NodeID: "node-a"},
			{SubscriptionID: "sub-b", NodeID: "node-b"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.AvailableCount != 2 || created.MemberCount != 2 {
		t.Fatalf("unexpected member counts: %+v", created)
	}
	pool, nodes, err := manager.Resolve(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pool.ProbeIntervalSeconds != 60 || pool.ToleranceMS != 80 || pool.ProbeURL != defaultProbeURL || pool.IdleTimeoutSeconds != 1800 ||
		pool.HighLatencyThresholdMS != 3000 || pool.ConsecutiveFailures != 2 || pool.RecoverySuccesses != 2 || pool.MaxBackoffSeconds != 300 || len(nodes) != 2 {
		t.Fatalf("unexpected resolved pool: pool=%+v nodes=%+v", pool, nodes)
	}

	reopened, err := OpenManager(poolDirectory, subscriptions)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.List(); len(got) != 1 || got[0].ID != created.ID || got[0].AvailableCount != 2 {
		t.Fatalf("unexpected reopened pools: %+v", got)
	}
}

func TestValidatePoolRejectsUnsafeHealthPolicy(t *testing.T) {
	t.Parallel()
	base := Pool{Name: "Test", ProbeIntervalSeconds: 60, IdleTimeoutSeconds: 1800}
	invalidFallback := base
	invalidFallback.FallbackProbeURLs = []string{"http://example.com/generate_204"}
	if _, err := validatePool(invalidFallback); err == nil {
		t.Fatal("validatePool() accepted an insecure fallback probe URL")
	}
	invalidTiming := base
	invalidTiming.ProbeIntervalSeconds = 300
	invalidTiming.IdleTimeoutSeconds = 60
	if _, err := validatePool(invalidTiming); err == nil {
		t.Fatal("validatePool() accepted probe interval greater than idle timeout")
	}
	for _, rawURL := range []string{
		"https://100.64.0.1/generate_204",
		"https://198.18.0.1/generate_204",
		"https://192.0.2.1/generate_204",
	} {
		invalidAddress := base
		invalidAddress.ProbeURL = rawURL
		if _, err := validatePool(invalidAddress); err == nil {
			t.Fatalf("validatePool() accepted non-public probe URL %q", rawURL)
		}
	}
}

func TestResolveRejectsPoolWithFewerThanTwoAvailableMembers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	subscriptions, err := subscription.OpenManager(filepath.Join(root, "subscriptions"), nil)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := OpenManager(filepath.Join(root, "pools"), subscriptions)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(CreateInput{Name: "Unavailable", Members: []Member{{SubscriptionID: "missing", NodeID: "one"}, {SubscriptionID: "missing", NodeID: "two"}}}); err == nil {
		t.Fatal("Create() accepted unavailable member")
	}
}

func TestReconcileSubscriptionNodesMigratesAndPrunesMembers(t *testing.T) {
	t.Parallel()
	oldStable := subscription.Node{ID: "old-stable", Name: "Old name", Type: "trojan", Server: "proxy.example.com", Port: 443, Password: "secret"}
	oldRemoved := subscription.Node{ID: "old-removed", Name: "Removed", Type: "trojan", Server: "removed.example.com", Port: 443, Password: "secret"}
	newStable := oldStable
	newStable.ID, newStable.Name = "new-stable", "New name"
	manager := &Manager{
		path: filepath.Join(t.TempDir(), "pools.json"),
		items: []Pool{{ID: "pool-1", Members: []Member{
			{SubscriptionID: "sub-1", NodeID: oldStable.ID},
			{SubscriptionID: "sub-1", NodeID: oldRemoved.ID},
			{SubscriptionID: "sub-2", NodeID: "other"},
		}}},
	}
	if err := manager.ReconcileSubscriptionNodes("sub-1", []subscription.Node{oldStable, oldRemoved}, []subscription.Node{newStable}); err != nil {
		t.Fatal(err)
	}
	got := manager.items[0].Members
	if len(got) != 2 || got[0].NodeID != newStable.ID || got[1].SubscriptionID != "sub-2" {
		t.Fatalf("reconciled members = %+v", got)
	}
	reloaded := &Manager{path: manager.path}
	if err := reloaded.load(); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.items[0].Members; len(got) != 2 || got[0].NodeID != newStable.ID {
		t.Fatalf("persisted reconciled members = %+v", got)
	}
}

func TestDeleteSubscriptionMembersRemovesEveryReference(t *testing.T) {
	t.Parallel()
	manager := &Manager{
		path: filepath.Join(t.TempDir(), "pools.json"),
		items: []Pool{
			{ID: "pool-1", Members: []Member{{SubscriptionID: "sub-1", NodeID: "one"}, {SubscriptionID: "sub-2", NodeID: "two"}}},
			{ID: "pool-2", Members: []Member{{SubscriptionID: "sub-1", NodeID: "three"}}},
		},
	}
	if err := manager.DeleteSubscriptionMembers("sub-1"); err != nil {
		t.Fatal(err)
	}
	if len(manager.items[0].Members) != 1 || manager.items[0].Members[0].SubscriptionID != "sub-2" || len(manager.items[1].Members) != 0 {
		t.Fatalf("subscription members were not removed: %+v", manager.items)
	}
}

func TestReorderPersistsNodePoolOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	subscriptions, err := subscription.OpenManager(filepath.Join(root, "subscriptions"), nil)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "pools")
	manager, err := OpenManager(directory, subscriptions)
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Create(CreateInput{Name: "Alpha"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create(CreateInput{Name: "Beta"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reorder([]string{second.ID, first.ID}); err != nil {
		t.Fatalf("Reorder() error = %v", err)
	}
	if listed := manager.List(); len(listed) != 2 || listed[0].ID != second.ID || listed[1].ID != first.ID {
		t.Fatalf("reordered pools = %+v", listed)
	}
	reopened, err := OpenManager(directory, subscriptions)
	if err != nil {
		t.Fatal(err)
	}
	if listed := reopened.List(); len(listed) != 2 || listed[0].ID != second.ID || listed[1].ID != first.ID {
		t.Fatalf("persisted pool order = %+v", listed)
	}
	if _, err := manager.Reorder([]string{first.ID, first.ID}); err == nil {
		t.Fatal("Reorder() accepted duplicate pool IDs")
	}
}
