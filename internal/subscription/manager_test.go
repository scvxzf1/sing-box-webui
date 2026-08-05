package subscription

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"sing-box-webui/internal/events"
)

type fakeRuleSink struct {
	syncErr   error
	deleteErr error
	reloads   int
}

func (s *fakeRuleSink) SyncSubscriptionRules(string, string, []ImportedRule) error { return s.syncErr }
func (s *fakeRuleSink) DeleteSubscriptionRules(string) error                       { return s.deleteErr }
func (s *fakeRuleSink) ReloadRules() error                                         { s.reloads++; return nil }

type fakeFetcher struct {
	content []byte
	err     error
}

func (f fakeFetcher) Fetch(context.Context, string, string, string) ([]byte, FetchMetadata, error) {
	return f.content, FetchMetadata{ETag: `"test"`}, f.err
}

func TestManagerSubscriptionWorkflow(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	manager := &Manager{
		path:    filepath.Join(directory, "subscriptions.json"),
		items:   []Subscription{},
		fetcher: fakeFetcher{content: []byte("trojan://secret@proxy.example.com:443?security=tls#Primary")},
		events:  events.NewBroker(16, 4),
	}

	created, err := manager.Create(context.Background(), CreateInput{
		Name:                  "Main",
		URL:                   "https://subscription.example.com/token?access=secret",
		AutoUpdate:            true,
		UpdateIntervalMinutes: 60,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !created.Active || created.NodeCount != 1 || created.SelectedNodeID == "" {
		t.Fatalf("unexpected created subscription: %+v", created)
	}
	if len(created.Nodes) != 1 || created.Nodes[0].Server != "proxy.example.com" {
		t.Fatalf("unexpected sanitized nodes: %+v", created.Nodes)
	}
	if created.URL != "https://subscription.example.com/token?access=secret" {
		t.Fatalf("detail URL = %q, want complete URL", created.URL)
	}
	listed := manager.List()
	if len(listed) != 1 || listed[0].URL != "https://subscription.example.com/token?redacted" {
		t.Fatalf("list URL = %q, want redacted URL", listed[0].URL)
	}

	if _, err := manager.SelectNode(created.ID, created.Nodes[0].ID); err != nil {
		t.Fatalf("SelectNode() error = %v", err)
	}
	if due := manager.dueForUpdate(time.Now().Add(2 * time.Hour)); len(due) != 1 || due[0] != created.ID {
		t.Fatalf("dueForUpdate() = %v, want %s", due, created.ID)
	}
	targets, err := manager.ProbeNodes(created.ID, []string{created.Nodes[0].ID})
	if err != nil || len(targets) != 1 || targets[0].Server != "proxy.example.com" {
		t.Fatalf("ProbeNodes() = %+v, %v", targets, err)
	}
	if _, err := manager.ProbeNodes(created.ID, []string{"missing"}); err == nil {
		t.Fatal("ProbeNodes() accepted an unknown node")
	}

	reloaded := &Manager{path: manager.path, fetcher: manager.fetcher, events: manager.events}
	if err := reloaded.load(); err != nil {
		t.Fatalf("reload subscriptions: %v", err)
	}
	if got := reloaded.List(); len(got) != 1 || got[0].NodeCount != 1 {
		t.Fatalf("reloaded subscriptions = %+v", got)
	}
}

func TestRedactURLHidesQuerySecrets(t *testing.T) {
	t.Parallel()
	redacted := redactURL("https://user:pass@example.com/sub?token=secret#fragment")
	if redacted != "https://example.com/sub?redacted" {
		t.Fatalf("redactURL() = %q", redacted)
	}
}

func TestCreateRejectsNonHTTPSubscriptionURL(t *testing.T) {
	t.Parallel()
	manager := &Manager{path: filepath.Join(t.TempDir(), "subscriptions.json"), items: []Subscription{}, fetcher: fakeFetcher{}}
	if _, err := manager.Create(context.Background(), CreateInput{Name: "Bad", URL: "file:///tmp/sub", UpdateIntervalMinutes: 60}); err == nil {
		t.Fatal("Create() accepted a non-HTTP subscription URL")
	}
}

func TestRefreshRollsBackSubscriptionWhenRuleSyncFails(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	sink := &fakeRuleSink{syncErr: errors.New("rule store unavailable")}
	manager := &Manager{
		path:     filepath.Join(directory, "subscriptions.json"),
		items:    []Subscription{{ID: "sub-1", Name: "Main", URL: "https://subscription.example.com", Nodes: []Node{{ID: "old", Name: "Old", Type: "trojan", Server: "old.example.com", Port: 443, Password: "secret"}}}},
		fetcher:  fakeFetcher{content: []byte("trojan://secret@new.example.com:443?security=tls#New")},
		ruleSink: sink,
	}
	if err := manager.persistLocked(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(context.Background(), "sub-1"); err == nil {
		t.Fatal("Refresh() succeeded despite rule sync failure")
	}
	view, err := manager.Get("sub-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Nodes) != 1 || view.Nodes[0].ID != "old" {
		t.Fatalf("subscription was not rolled back: %#v", view.Nodes)
	}
}

func TestDeleteRollsBackWhenRuleDeletionFails(t *testing.T) {
	t.Parallel()
	manager := &Manager{
		path:     filepath.Join(t.TempDir(), "subscriptions.json"),
		items:    []Subscription{{ID: "sub-1", Name: "Main", URL: "https://subscription.example.com", Active: true}},
		ruleSink: &fakeRuleSink{deleteErr: errors.New("rule store unavailable")},
	}
	if err := manager.persistLocked(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete("sub-1"); err == nil {
		t.Fatal("Delete() succeeded despite rule deletion failure")
	}
	if len(manager.List()) != 1 {
		t.Fatal("subscription deletion was not rolled back")
	}
}
