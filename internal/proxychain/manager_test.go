package proxychain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"sing-box-webui/internal/nodepool"
	"sing-box-webui/internal/subscription"
)

func TestManagerPersistsAndResolvesNodeAndPoolChains(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	subscriptions := openSubscriptions(t, root)
	pools, err := nodepool.OpenManager(filepath.Join(root, "pools"), subscriptions)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pools.Create(nodepool.CreateInput{Name: "Entry pool", Members: []nodepool.Member{
		{SubscriptionID: "sub-1", NodeID: "entry-1"},
		{SubscriptionID: "sub-1", NodeID: "entry-2"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "chains")
	manager, err := OpenManager(directory, subscriptions, pools)
	if err != nil {
		t.Fatal(err)
	}
	nodeChain, err := manager.Create(CreateInput{
		Name: "Node chain", EntryType: EntryNode,
		EntryNode: NodeRef{SubscriptionID: "sub-1", NodeID: "entry-1"},
		ExitNode:  NodeRef{SubscriptionID: "sub-1", NodeID: "exit"},
	})
	if err != nil {
		t.Fatal(err)
	}
	poolChain, err := manager.Create(CreateInput{
		Name: "Pool chain", EntryType: EntryPool, EntryPoolID: pool.ID,
		ExitNode: NodeRef{SubscriptionID: "sub-1", NodeID: "exit"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !nodeChain.Available || nodeChain.EntryName != "Entry one" || nodeChain.ExitName != "Exit" {
		t.Fatalf("unexpected node chain view: %+v", nodeChain)
	}
	if !poolChain.Available || poolChain.EntryName != "Entry pool" || poolChain.EntryMemberCount != 2 {
		t.Fatalf("unexpected pool chain view: %+v", poolChain)
	}
	resolved, err := manager.Resolve(poolChain.ID)
	if err != nil || len(resolved.EntryNodes) != 2 || resolved.ExitNode.ID != "exit" {
		t.Fatalf("unexpected resolved pool chain: resolved=%+v err=%v", resolved, err)
	}
	reopened, err := OpenManager(directory, subscriptions, pools)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.List(); len(got) != 2 || got[0].ID != nodeChain.ID || got[1].ID != poolChain.ID {
		t.Fatalf("unexpected persisted chains: %+v", got)
	}
}

func TestManagerRejectsSelfReferences(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	subscriptions := openSubscriptions(t, root)
	pools, err := nodepool.OpenManager(filepath.Join(root, "pools"), subscriptions)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := OpenManager(filepath.Join(root, "chains"), subscriptions, pools)
	if err != nil {
		t.Fatal(err)
	}
	ref := NodeRef{SubscriptionID: "sub-1", NodeID: "entry-1"}
	if _, err := manager.Create(CreateInput{Name: "Self", EntryType: EntryNode, EntryNode: ref, ExitNode: ref}); err == nil {
		t.Fatal("Create() accepted the same entry and exit node")
	}
	pool, err := pools.Create(nodepool.CreateInput{Name: "Contains exit", Members: []nodepool.Member{
		{SubscriptionID: "sub-1", NodeID: "entry-1"}, {SubscriptionID: "sub-1", NodeID: "entry-2"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(CreateInput{Name: "Pool self", EntryType: EntryPool, EntryPoolID: pool.ID, ExitNode: ref}); err == nil {
		t.Fatal("Create() accepted a pool containing the exit node")
	}
}

func openSubscriptions(t *testing.T, root string) *subscription.Manager {
	t.Helper()
	directory := filepath.Join(root, "subscriptions")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal([]subscription.Subscription{{
		ID: "sub-1", Name: "Primary", Nodes: []subscription.Node{
			{ID: "entry-1", Name: "Entry one", Type: "shadowsocks", Server: "1.1.1.1", Port: 443, Method: "aes-128-gcm", Password: "one"},
			{ID: "entry-2", Name: "Entry two", Type: "trojan", Server: "8.8.8.8", Port: 443, Password: "two"},
			{ID: "exit", Name: "Exit", Type: "shadowsocks", Server: "9.9.9.9", Port: 443, Method: "aes-128-gcm", Password: "exit"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "subscriptions.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := subscription.OpenManager(directory, nil)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
