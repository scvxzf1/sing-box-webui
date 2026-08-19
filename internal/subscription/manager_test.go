package subscription

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
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

type blockingRuleSink struct {
	syncStarted chan struct{}
	releaseSync chan struct{}
	deleted     chan struct{}
	mu          sync.Mutex
	rulesExist  bool
}

func (s *blockingRuleSink) SyncSubscriptionRules(string, string, []ImportedRule) error {
	close(s.syncStarted)
	<-s.releaseSync
	s.mu.Lock()
	s.rulesExist = true
	s.mu.Unlock()
	return nil
}

func (s *blockingRuleSink) DeleteSubscriptionRules(string) error {
	s.mu.Lock()
	s.rulesExist = false
	s.mu.Unlock()
	close(s.deleted)
	return nil
}

func (s *blockingRuleSink) ReloadRules() error { return nil }

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

func TestCreateRollsBackWhenInitialRefreshFails(t *testing.T) {
	t.Parallel()
	manager := &Manager{
		path:    filepath.Join(t.TempDir(), "subscriptions.json"),
		items:   []Subscription{},
		fetcher: fakeFetcher{err: errors.New("upstream unavailable")},
	}
	if _, err := manager.Create(context.Background(), CreateInput{Name: "Broken", URL: "https://subscription.example.com", UpdateIntervalMinutes: 60}); err == nil {
		t.Fatal("Create() succeeded despite initial refresh failure")
	}
	if listed := manager.List(); len(listed) != 0 {
		t.Fatalf("failed create left subscriptions: %+v", listed)
	}
}

func TestCreateRollsBackWhenInitialRuleSyncFails(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	manager := &Manager{
		path: filepath.Join(directory, "subscriptions.json"), items: []Subscription{},
		fetcher:  fakeFetcher{content: []byte("trojan://secret@proxy.example.com:443?security=tls#Primary")},
		ruleSink: &fakeRuleSink{syncErr: errors.New("rule store unavailable")},
	}
	if _, err := manager.Create(context.Background(), CreateInput{Name: "Broken", URL: "https://subscription.example.com", UpdateIntervalMinutes: 60}); err == nil {
		t.Fatal("Create() succeeded despite initial rule sync failure")
	}
	if listed := manager.List(); len(listed) != 0 {
		t.Fatalf("failed create left subscriptions: %+v", listed)
	}
	reloaded := &Manager{path: manager.path}
	if err := reloaded.load(); err != nil {
		t.Fatal(err)
	}
	if listed := reloaded.List(); len(listed) != 0 {
		t.Fatalf("failed create remained on disk: %+v", listed)
	}
}

func TestDeleteWaitsForRefreshRuleSync(t *testing.T) {
	t.Parallel()
	sink := &blockingRuleSink{
		syncStarted: make(chan struct{}), releaseSync: make(chan struct{}), deleted: make(chan struct{}),
	}
	manager := &Manager{
		path:     filepath.Join(t.TempDir(), "subscriptions.json"),
		items:    []Subscription{{ID: "sub-1", Name: "Main", URL: "https://subscription.example.com"}},
		fetcher:  fakeFetcher{content: []byte("trojan://secret@proxy.example.com:443?security=tls#Primary")},
		ruleSink: sink,
	}
	if err := manager.persistLocked(); err != nil {
		t.Fatal(err)
	}
	refreshDone := make(chan error, 1)
	go func() { refreshDone <- manager.Refresh(context.Background(), "sub-1") }()
	<-sink.syncStarted
	deleteDone := make(chan error, 1)
	go func() { deleteDone <- manager.Delete("sub-1") }()
	select {
	case <-sink.deleted:
		t.Fatal("Delete() removed rules before the refresh rule sync completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(sink.releaseSync)
	if err := <-refreshDone; err != nil {
		t.Fatal(err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	rulesExist := sink.rulesExist
	sink.mu.Unlock()
	if rulesExist {
		t.Fatal("deleted subscription rules were resurrected")
	}
}

func TestRefreshLocksAreReclaimed(t *testing.T) {
	t.Parallel()
	manager := &Manager{
		path:    filepath.Join(t.TempDir(), "subscriptions.json"),
		items:   []Subscription{{ID: "sub-1", Name: "Main", URL: "https://subscription.example.com"}},
		fetcher: fakeFetcher{err: errors.New("upstream unavailable")},
	}
	_ = manager.Refresh(context.Background(), "sub-1")
	_ = manager.Refresh(context.Background(), "missing")
	manager.refreshMu.Lock()
	count := len(manager.refreshLocks)
	manager.refreshMu.Unlock()
	if count != 0 {
		t.Fatalf("refresh lock registry contains %d idle entries", count)
	}
}

func TestRefreshRetainsSubscriptionWhenRuleSyncFails(t *testing.T) {
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
	if len(view.Nodes) != 1 || view.Nodes[0].ID == "old" {
		t.Fatalf("subscription update was lost: %#v", view.Nodes)
	}
}

func TestDeleteCommitsWhenRuleDeletionFails(t *testing.T) {
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
	if len(manager.List()) != 0 {
		t.Fatal("subscription deletion was not committed")
	}
}

func TestReorderPersistsSubscriptionOrder(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	manager := &Manager{
		path: filepath.Join(directory, "subscriptions.json"),
		items: []Subscription{
			{ID: "sub-a", Name: "Alpha", URL: "https://a.example.com"},
			{ID: "sub-b", Name: "Beta", URL: "https://b.example.com"},
		},
	}
	if err := manager.persistLocked(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reorder([]string{"sub-b", "sub-a"}); err != nil {
		t.Fatalf("Reorder() error = %v", err)
	}
	if listed := manager.List(); len(listed) != 2 || listed[0].ID != "sub-b" || listed[1].ID != "sub-a" {
		t.Fatalf("reordered subscriptions = %+v", listed)
	}
	reloaded := &Manager{path: manager.path}
	if err := reloaded.load(); err != nil {
		t.Fatal(err)
	}
	if listed := reloaded.List(); len(listed) != 2 || listed[0].ID != "sub-b" || listed[1].ID != "sub-a" {
		t.Fatalf("persisted subscription order = %+v", listed)
	}
	if _, err := manager.Reorder([]string{"sub-a", "sub-a"}); err == nil {
		t.Fatal("Reorder() accepted duplicate subscription IDs")
	}
}

func TestRefreshFallsBackToProxyWhenDirectFails(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	manager := &Manager{
		path:     filepath.Join(directory, "subscriptions.json"),
		items:    []Subscription{{ID: "sub-1", Name: "Main", URL: "https://subscription.example.com", Nodes: []Node{}}},
		fetcher:  fakeFetcher{err: errors.New("direct unreachable")},
		events:   events.NewBroker(16, 4),
		ruleSink: &fakeRuleSink{},
	}
	if err := manager.persistLocked(); err != nil {
		t.Fatal(err)
	}
	manager.SetProxyResolver(func() string { return "127.0.0.1:2080" })
	manager.proxyFetcher = fakeFetcher{content: []byte("trojan://secret@via-proxy.example.com:443?security=tls#Proxy")}
	manager.proxyAddress = "127.0.0.1:2080"

	if err := manager.Refresh(context.Background(), "sub-1"); err != nil {
		t.Fatalf("Refresh() error = %v, want proxy fallback to succeed", err)
	}
	view, err := manager.Get("sub-1")
	if err != nil {
		t.Fatal(err)
	}
	if view.NodeCount != 1 || view.Nodes[0].Server != "via-proxy.example.com" {
		t.Fatalf("expected nodes from proxy fetch, got %+v", view.Nodes)
	}
	if view.LastFetchPath != "proxy" {
		t.Fatalf("LastFetchPath = %q, want proxy", view.LastFetchPath)
	}
}

func TestRefreshSkipsProxyWhenNoProxyAvailable(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	manager := &Manager{
		path:    filepath.Join(directory, "subscriptions.json"),
		items:   []Subscription{{ID: "sub-1", Name: "Main", URL: "https://subscription.example.com", Nodes: []Node{}}},
		fetcher: fakeFetcher{err: errors.New("direct unreachable")},
		events:  events.NewBroker(16, 4),
	}
	if err := manager.persistLocked(); err != nil {
		t.Fatal(err)
	}
	// Resolver returns empty (TUN mode / proxy not running): no fallback.
	manager.SetProxyResolver(func() string { return "" })

	err := manager.Refresh(context.Background(), "sub-1")
	if err == nil {
		t.Fatal("Refresh() succeeded despite direct failure and no proxy")
	}
	if got := err.Error(); got != "direct unreachable" {
		t.Fatalf("Refresh() error = %q, want the direct error only", got)
	}
	view, getErr := manager.Get("sub-1")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if view.LastError != "direct unreachable" {
		t.Fatalf("LastError = %q, want direct error recorded", view.LastError)
	}
}

func TestMutationsRollBackWhenPersistenceFails(t *testing.T) {
	t.Parallel()
	manager := &Manager{
		path: filepath.Join(t.TempDir(), "subscriptions.json"),
		items: []Subscription{{
			ID: "sub-1", Name: "Original", URL: "https://subscription.example.com",
			Active: true, Nodes: []Node{{ID: "node-1", Name: "Node", Type: "trojan"}},
		}},
	}
	if err := manager.persistLocked(); err != nil {
		t.Fatal(err)
	}
	manager.path = filepath.Join(filepath.Dir(manager.path), "missing", "subscriptions.json")
	name := "Changed"
	if _, err := manager.Update("sub-1", UpdateInput{Name: &name}); err == nil {
		t.Fatal("Update() succeeded with an unavailable store")
	}
	if got, _ := manager.Get("sub-1"); got.Name != "Original" {
		t.Fatalf("Update() changed memory after persistence failure: %+v", got)
	}
	if _, err := manager.SelectNode("sub-1", "node-1"); err == nil {
		t.Fatal("SelectNode() succeeded with an unavailable store")
	}
	if got, _ := manager.Get("sub-1"); got.SelectedNodeID != "" {
		t.Fatalf("SelectNode() changed memory after persistence failure: %+v", got)
	}
}
