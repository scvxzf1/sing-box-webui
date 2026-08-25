package proxychannel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"sing-box-webui/internal/subscription"
)

func TestManagerPersistsAndResolvesChannels(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	subscriptions := openSubscriptions(t, root)
	directory := filepath.Join(root, "channels")
	manager, err := OpenManager(directory, subscriptions, 2080)
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create(CreateInput{
		Name: "Local SOCKS", Protocol: ProtocolSOCKS5, Direction: DirectionForward, Port: 1080,
		Node: NodeRef{SubscriptionID: "sub-1", NodeID: "node-1"}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created.Available || created.NodeName != "Tokyo" || created.ListenAddress != "127.0.0.1:1080" {
		t.Fatalf("unexpected channel view: %+v", created)
	}
	if len(created.AccessAddresses) != 1 || created.AccessAddresses[0] != "127.0.0.1:1080" {
		t.Fatalf("access addresses = %v", created.AccessAddresses)
	}
	resolved := manager.ResolveEnabled()
	if len(resolved) != 1 || resolved[0].Node.ID != "node-1" {
		t.Fatalf("unexpected resolved channels: %+v", resolved)
	}
	reopened, err := OpenManager(directory, subscriptions, 2080)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.List(); len(got) != 1 || got[0].ID != created.ID {
		t.Fatalf("unexpected persisted channels: %+v", got)
	}
	if certificate, err := reopened.Certificate(); err != nil || len(certificate) == 0 {
		t.Fatalf("missing HTTPS channel certificate: bytes=%d err=%v", len(certificate), err)
	}
}

func TestManagerValidatesSharedAuthenticationAndPorts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	manager, err := OpenManager(filepath.Join(root, "channels"), openSubscriptions(t, root), 2080)
	if err != nil {
		t.Fatal(err)
	}
	base := CreateInput{
		Name: "Shared HTTP", Protocol: ProtocolHTTP, Direction: DirectionReverse, Port: 18080,
		Node: NodeRef{SubscriptionID: "sub-1", NodeID: "node-1"}, Enabled: true,
	}
	if _, err := manager.Create(base); err == nil {
		t.Fatal("Create() accepted an unauthenticated reverse channel")
	}
	base.Username, base.Password = "app", "secret"
	created, err := manager.Create(base)
	if err != nil {
		t.Fatal(err)
	}
	if created.ListenAddress != "0.0.0.0:18080" {
		t.Fatalf("listen address = %q", created.ListenAddress)
	}
	for _, address := range created.AccessAddresses {
		if address == created.ListenAddress {
			t.Fatalf("shared access address must not expose wildcard listener: %v", created.AccessAddresses)
		}
	}
	base.Name = "Duplicate"
	if _, err := manager.Create(base); err == nil {
		t.Fatal("Create() accepted a duplicate port")
	}
	base.Name, base.Port = "Reserved", 2080
	if _, err := manager.Create(base); err == nil {
		t.Fatal("Create() accepted a reserved port")
	}
}

func openSubscriptions(t *testing.T, root string) *subscription.Manager {
	t.Helper()
	directory := filepath.Join(root, "subscriptions")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal([]subscription.Subscription{{
		ID: "sub-1", Name: "Primary", Nodes: []subscription.Node{{
			ID: "node-1", Name: "Tokyo", Type: "shadowsocks", Server: "1.1.1.1", Port: 443,
			Method: "aes-128-gcm", Password: "secret",
		}},
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
