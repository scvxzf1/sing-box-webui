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
			{SubscriptionID: "missing", NodeID: "missing"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.AvailableCount != 2 || created.MemberCount != 3 {
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
	created, err := manager.Create(CreateInput{Name: "Unavailable", Members: []Member{{SubscriptionID: "missing", NodeID: "one"}, {SubscriptionID: "missing", NodeID: "two"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Resolve(created.ID); err == nil {
		t.Fatal("Resolve() error = nil, want unavailable member error")
	}
}
