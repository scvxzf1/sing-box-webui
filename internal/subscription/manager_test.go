package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sing-box-webui/internal/events"
)

type fakeRuleSink struct {
	syncErr     error
	deleteErr   error
	deleteCalls int
	reloads     int
}

func (s *fakeRuleSink) SyncSubscriptionRules(string, string, []ImportedRule) error { return s.syncErr }
func (s *fakeRuleSink) DeleteSubscriptionRules(string) error {
	s.deleteCalls++
	return s.deleteErr
}
func (s *fakeRuleSink) ReloadRules() error { s.reloads++; return nil }

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
	if created.URL != "https://subscription.example.com/token?redacted" {
		t.Fatalf("detail URL = %q, want redacted URL", created.URL)
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

func TestRedactURLErrorHidesQuerySecrets(t *testing.T) {
	t.Parallel()
	err := &url.Error{
		Op:  "Get",
		URL: "https://subscription.example.com/token?access=secret",
		Err: errors.New("upstream unavailable"),
	}
	redacted := redactURLError(err)
	if strings.Contains(redacted.Error(), "secret") || !strings.Contains(redacted.Error(), "?redacted") {
		t.Fatalf("redactURLError() = %q", redacted)
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

func TestImportNodesPersistsPartialResultsAndSurvivesRefresh(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	upstream, err := ParseNodeLink("trojan://upstream@upstream.example.com:443#Upstream")
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		path: filepath.Join(directory, "subscriptions.json"),
		items: []Subscription{{
			ID: "sub-1", Name: "Main", URL: "https://subscription.example.com", Active: true,
			SelectedNodeID: upstream.ID, Nodes: []Node{upstream},
		}},
		fetcher:  fakeFetcher{content: []byte("socks://new.example.com:1080#Refreshed")},
		events:   events.NewBroker(16, 4),
		ruleSink: &fakeRuleSink{},
	}
	if err := manager.persistLocked(); err != nil {
		t.Fatal(err)
	}

	const manualLink = "trojan://manual-secret@manual.example.com:443#Manual"
	result, err := manager.ImportNodes("sub-1", ImportNodesInput{Links: manualLink + "\n" + manualLink + "\nftp://hidden@invalid.example.com:21"})
	if err != nil {
		t.Fatalf("ImportNodes() error = %v", err)
	}
	if result.AddedCount != 1 || result.DuplicateCount != 1 || result.InvalidCount != 1 || len(result.Items) != 3 {
		t.Fatalf("ImportNodes() result = %+v", result)
	}
	if result.Items[0].Status != "added" || result.Items[0].Node == nil || result.Items[1].Status != "duplicate" || result.Items[2].Status != "invalid" {
		t.Fatalf("ImportNodes() items = %+v", result.Items)
	}
	if strings.Contains(result.Items[2].Error, "hidden") {
		t.Fatalf("invalid item leaked credentials: %q", result.Items[2].Error)
	}
	if result.Subscription.NodeCount != 2 {
		t.Fatalf("imported subscription = %+v", result.Subscription)
	}

	reloaded := &Manager{path: manager.path, fetcher: manager.fetcher, events: manager.events, ruleSink: manager.ruleSink}
	if err := reloaded.load(); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.items) != 1 || len(reloaded.items[0].ManualNodeIDs) != 1 {
		t.Fatalf("persisted manual node IDs = %+v", reloaded.items)
	}
	if err := reloaded.Refresh(context.Background(), "sub-1"); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	view, err := reloaded.Get("sub-1")
	if err != nil {
		t.Fatal(err)
	}
	if view.NodeCount != 2 {
		t.Fatalf("refreshed nodes = %+v", view.Nodes)
	}
	foundManual := false
	for _, node := range view.Nodes {
		foundManual = foundManual || node.Name == "Manual"
	}
	if !foundManual {
		t.Fatalf("manual node was lost after refresh: %+v", view.Nodes)
	}
}

func TestImportNodesRefreshesExistingNodeMetadata(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	const link = "tuic://2cf8b091-58eb-40e4-8524-bc45142c400e:secret@107.149.30.19:8445?allow_insecure=1#TUIC"
	node, err := ParseNodeLink(link)
	if err != nil {
		t.Fatal(err)
	}
	node.TLS.Insecure = false
	manager := &Manager{
		path: filepath.Join(directory, "subscriptions.json"),
		items: []Subscription{{
			ID: "sub-1", Name: "Main", URL: "https://subscription.example.com",
			SelectedNodeID: node.ID, Nodes: []Node{node}, ManualNodeIDs: []string{node.ID},
		}},
		events: events.NewBroker(16, 4),
	}
	if err := manager.persistLocked(); err != nil {
		t.Fatal(err)
	}

	result, err := manager.ImportNodes("sub-1", ImportNodesInput{Links: link})
	if err != nil {
		t.Fatalf("ImportNodes() error = %v", err)
	}
	if result.DuplicateCount != 1 || result.AddedCount != 0 {
		t.Fatalf("ImportNodes() result = %+v", result)
	}
	if len(manager.items[0].Nodes) != 1 || !manager.items[0].Nodes[0].TLS.Insecure {
		t.Fatalf("existing node was not refreshed: %+v", manager.items[0].Nodes)
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

func TestDeleteRetriesDependencyCleanupAfterSubscriptionIsGone(t *testing.T) {
	t.Parallel()
	sink := &fakeRuleSink{deleteErr: errors.New("rule store unavailable")}
	manager := &Manager{
		path:     filepath.Join(t.TempDir(), "subscriptions.json"),
		items:    []Subscription{{ID: "sub-1", Name: "Main", URL: "https://subscription.example.com", Active: true}},
		ruleSink: sink,
	}
	if err := manager.persistLocked(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete("sub-1"); err == nil {
		t.Fatal("Delete() succeeded despite rule deletion failure")
	}
	sink.deleteErr = nil
	if err := manager.Delete("sub-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete() error = %v, want ErrNotFound after cleanup", err)
	}
	if sink.deleteCalls != 2 || sink.reloads != 1 {
		t.Fatalf("cleanup calls = %d, reloads = %d; want 2 and 1", sink.deleteCalls, sink.reloads)
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

func TestRefreshErrorRedactsSubscriptionURLSecrets(t *testing.T) {
	t.Parallel()
	const rawURL = "https://subscription.example.com/token?access=secret"
	broker := events.NewBroker(16, 4)
	manager := &Manager{
		path:    filepath.Join(t.TempDir(), "subscriptions.json"),
		items:   []Subscription{{ID: "sub-1", Name: "Main", URL: rawURL}},
		fetcher: fakeFetcher{err: fmt.Errorf(`Get "https://subscription.example.com/token?access=secret": %w`, context.DeadlineExceeded)},
		events:  broker,
	}
	if err := manager.persistLocked(); err != nil {
		t.Fatal(err)
	}

	refreshErr := manager.Refresh(context.Background(), "sub-1")
	if refreshErr == nil || strings.Contains(refreshErr.Error(), "secret") {
		t.Fatalf("Refresh() error leaked URL secret: %v", refreshErr)
	}
	if !errors.Is(refreshErr, context.DeadlineExceeded) {
		t.Fatalf("Refresh() error lost deadline cause: %v", refreshErr)
	}
	view, err := manager.Get("sub-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(view.LastError, "secret") {
		t.Fatalf("view LastError leaked URL secret: %q", view.LastError)
	}
	persisted, err := os.ReadFile(manager.path)
	if err != nil {
		t.Fatal(err)
	}
	var stored []Subscription
	if err := json.Unmarshal(persisted, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || strings.Contains(stored[0].LastError, "secret") {
		t.Fatalf("persisted LastError leaked URL secret: %q", stored[0].LastError)
	}

	history := broker.History()
	if len(history) == 0 || history[len(history)-1].Type != "subscription.failed" {
		t.Fatalf("subscription.failed event not published: %+v", history)
	}
	var payload map[string]string
	if err := json.Unmarshal(history[len(history)-1].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload["error"], "secret") {
		t.Fatalf("event payload leaked URL secret: %q", payload["error"])
	}
}

func TestLoadRedactsPersistedSubscriptionErrors(t *testing.T) {
	t.Parallel()
	const rawURL = "https://subscription.example.com/token?access=secret"
	path := filepath.Join(t.TempDir(), "subscriptions.json")
	content, err := json.Marshal([]Subscription{{
		ID:        "sub-1",
		Name:      "Main",
		URL:       rawURL,
		LastError: `Get "https://subscription.example.com/token?access=secret": upstream unavailable`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	manager := &Manager{path: path}
	if err := manager.load(); err != nil {
		t.Fatal(err)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored []Subscription
	if err := json.Unmarshal(persisted, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || strings.Contains(stored[0].LastError, "secret") {
		t.Fatalf("load() retained a URL secret in LastError: %q", stored[0].LastError)
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
