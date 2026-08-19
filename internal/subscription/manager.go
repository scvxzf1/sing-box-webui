package subscription

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sing-box-webui/internal/events"
)

const (
	defaultUpdateMinutes = 360
	minUpdateMinutes     = 15
	maxUpdateMinutes     = 7 * 24 * 60
)

var ErrNotFound = errors.New("subscription not found")

type CreateInput struct {
	Name                  string `json:"name"`
	URL                   string `json:"url"`
	AutoUpdate            bool   `json:"autoUpdate"`
	UpdateIntervalMinutes int    `json:"updateIntervalMinutes"`
}

type UpdateInput struct {
	Name                  *string `json:"name,omitempty"`
	AutoUpdate            *bool   `json:"autoUpdate,omitempty"`
	UpdateIntervalMinutes *int    `json:"updateIntervalMinutes,omitempty"`
}

type RuleSink interface {
	SyncSubscriptionRules(subscriptionID, subscriptionName string, rules []ImportedRule) error
	DeleteSubscriptionRules(subscriptionID string) error
	ReloadRules() error
}

type Manager struct {
	mu           sync.RWMutex
	refreshMu    sync.Mutex
	refreshLocks map[string]*sync.Mutex
	path         string
	items        []Subscription
	fetcher      FetchClient
	parser       Parser
	events       *events.Broker
	ruleSink     RuleSink

	proxyResolver func() string
	proxyFetcher  FetchClient
	proxyAddress  string
}

func (m *Manager) SetRuleSink(sink RuleSink) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ruleSink = sink
}

// SetProxyResolver injects a source for the local proxy address (host:port)
// used as a fetch fallback when the direct request fails. The resolver is
// expected to return an empty string when no usable proxy is running.
func (m *Manager) SetProxyResolver(resolve func() string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.proxyResolver = resolve
}

func OpenManager(dataDirectory string, broker *events.Broker) (*Manager, error) {
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create subscription data directory: %w", err)
	}
	if err := os.Chmod(dataDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("set subscription data permissions: %w", err)
	}
	manager := &Manager{
		path:    filepath.Join(dataDirectory, "subscriptions.json"),
		fetcher: NewFetcher(),
		events:  broker,
	}
	if err := manager.load(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) List() []View {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.listLocked()
}

func (m *Manager) listLocked() []View {
	views := make([]View, 0, len(m.items))
	for _, item := range m.items {
		views = append(views, toView(item, false))
	}
	return views
}

func (m *Manager) Reorder(ids []string) ([]View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(ids) != len(m.items) {
		return nil, fmt.Errorf("order must include every subscription exactly once")
	}
	indices := make(map[string]int, len(m.items))
	for index, item := range m.items {
		indices[item.ID] = index
	}
	seen := make(map[string]struct{}, len(ids))
	ordered := make([]Subscription, len(ids))
	for position, id := range ids {
		index, exists := indices[id]
		if !exists {
			return nil, fmt.Errorf("order contains an unknown subscription")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("order contains a duplicate subscription")
		}
		seen[id] = struct{}{}
		ordered[position] = m.items[index]
	}
	previous := m.items
	m.items = ordered
	if err := m.persistLocked(); err != nil {
		m.items = previous
		return nil, err
	}
	m.publish("subscriptions.reordered", map[string]int{"count": len(ids)})
	return m.listLocked(), nil
}

func (m *Manager) Get(id string) (View, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	index := m.indexOf(id)
	if index < 0 {
		return View{}, ErrNotFound
	}
	return toView(m.items[index], true), nil
}

func (m *Manager) Create(ctx context.Context, input CreateInput) (View, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.URL = strings.TrimSpace(input.URL)
	if input.Name == "" || len(input.Name) > 80 {
		return View{}, fmt.Errorf("subscription name must contain 1-80 characters")
	}
	parsedURL, err := url.Parse(input.URL)
	if err != nil || validateSubscriptionURL(parsedURL) != nil {
		return View{}, fmt.Errorf("invalid subscription URL")
	}
	if input.UpdateIntervalMinutes == 0 {
		input.UpdateIntervalMinutes = defaultUpdateMinutes
	}
	if err := validateInterval(input.UpdateIntervalMinutes); err != nil {
		return View{}, err
	}

	m.mu.Lock()
	for _, item := range m.items {
		if item.URL == input.URL {
			m.mu.Unlock()
			return View{}, fmt.Errorf("subscription URL already exists")
		}
	}
	item := Subscription{
		ID:                    randomID(),
		Name:                  input.Name,
		URL:                   input.URL,
		AutoUpdate:            input.AutoUpdate,
		UpdateIntervalMinutes: input.UpdateIntervalMinutes,
		Active:                len(m.items) == 0,
		Nodes:                 []Node{},
	}
	m.items = append(m.items, item)
	if err := m.persistLocked(); err != nil {
		m.items = m.items[:len(m.items)-1]
		m.mu.Unlock()
		return View{}, err
	}
	m.mu.Unlock()

	if err := m.Refresh(ctx, item.ID); err != nil {
		return View{}, err
	}
	return m.Get(item.ID)
}

func (m *Manager) Update(id string, input UpdateInput) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.indexOf(id)
	if index < 0 {
		return View{}, ErrNotFound
	}
	previous := m.items[index]
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" || len(name) > 80 {
			return View{}, fmt.Errorf("subscription name must contain 1-80 characters")
		}
		m.items[index].Name = name
	}
	if input.AutoUpdate != nil {
		m.items[index].AutoUpdate = *input.AutoUpdate
	}
	if input.UpdateIntervalMinutes != nil {
		if err := validateInterval(*input.UpdateIntervalMinutes); err != nil {
			return View{}, err
		}
		m.items[index].UpdateIntervalMinutes = *input.UpdateIntervalMinutes
	}
	if err := m.persistLocked(); err != nil {
		m.items[index] = previous
		return View{}, err
	}
	return toView(m.items[index], true), nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	index := m.indexOf(id)
	if index < 0 {
		m.mu.Unlock()
		return ErrNotFound
	}
	previousItems := append([]Subscription(nil), m.items...)
	wasActive := m.items[index].Active
	m.items = append(m.items[:index], m.items[index+1:]...)
	if wasActive && len(m.items) > 0 {
		m.items[0].Active = true
	}
	if err := m.persistLocked(); err != nil {
		m.items = previousItems
		m.mu.Unlock()
		return err
	}
	sink := m.ruleSink
	m.mu.Unlock()
	if sink != nil {
		if err := sink.DeleteSubscriptionRules(id); err != nil {
			return fmt.Errorf("subscription deleted but rules cleanup failed: %w", err)
		}
		return sink.ReloadRules()
	}
	return nil
}

func (m *Manager) Activate(id string) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.indexOf(id)
	if index < 0 {
		return View{}, ErrNotFound
	}
	previous := append([]Subscription(nil), m.items...)
	for itemIndex := range m.items {
		m.items[itemIndex].Active = itemIndex == index
	}
	if err := m.persistLocked(); err != nil {
		m.items = previous
		return View{}, err
	}
	m.publish("subscription.activated", map[string]string{"subscriptionId": id})
	return toView(m.items[index], true), nil
}

func (m *Manager) SelectNode(subscriptionID, nodeID string) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.indexOf(subscriptionID)
	if index < 0 {
		return View{}, ErrNotFound
	}
	found := false
	for _, node := range m.items[index].Nodes {
		if node.ID == nodeID {
			found = true
			break
		}
	}
	if !found {
		return View{}, fmt.Errorf("node not found")
	}
	previous := m.items[index].SelectedNodeID
	m.items[index].SelectedNodeID = nodeID
	if err := m.persistLocked(); err != nil {
		m.items[index].SelectedNodeID = previous
		return View{}, err
	}
	m.publish("node.selected", map[string]string{"subscriptionId": subscriptionID, "nodeId": nodeID})
	return toView(m.items[index], true), nil
}

func (m *Manager) SelectedNode(subscriptionID, nodeID string) (Subscription, Node, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	index := m.indexOf(subscriptionID)
	if index < 0 {
		return Subscription{}, Node{}, ErrNotFound
	}
	if nodeID == "" {
		nodeID = m.items[index].SelectedNodeID
	}
	for _, node := range m.items[index].Nodes {
		if node.ID == nodeID {
			return m.items[index], node, nil
		}
	}
	return Subscription{}, Node{}, fmt.Errorf("selected node not found")
}

func (m *Manager) ProbeNodes(subscriptionID string, nodeIDs []string) ([]Node, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	index := m.indexOf(subscriptionID)
	if index < 0 {
		return nil, ErrNotFound
	}

	byID := make(map[string]Node, len(m.items[index].Nodes))
	for _, node := range m.items[index].Nodes {
		byID[node.ID] = node
	}
	if len(nodeIDs) == 0 {
		nodeIDs = make([]string, 0, len(m.items[index].Nodes))
		for _, node := range m.items[index].Nodes {
			nodeIDs = append(nodeIDs, node.ID)
		}
	}

	nodes := make([]Node, 0, len(nodeIDs))
	seen := make(map[string]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if _, duplicate := seen[nodeID]; duplicate {
			continue
		}
		node, exists := byID[nodeID]
		if !exists {
			return nil, fmt.Errorf("node %q not found", nodeID)
		}
		seen[nodeID] = struct{}{}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func (m *Manager) Refresh(ctx context.Context, id string) error {
	return m.refresh(ctx, id, false)
}

func (m *Manager) refresh(ctx context.Context, id string, conditional bool) error {
	// Serialize refreshes so a slower request cannot overwrite a newer parse or
	// publish a stale failure after a successful refresh.
	m.refreshMu.Lock()
	if m.refreshLocks == nil {
		m.refreshLocks = make(map[string]*sync.Mutex)
	}
	refreshLock := m.refreshLocks[id]
	if refreshLock == nil {
		refreshLock = &sync.Mutex{}
		m.refreshLocks[id] = refreshLock
	}
	m.refreshMu.Unlock()
	refreshLock.Lock()
	defer refreshLock.Unlock()

	m.mu.RLock()
	index := m.indexOf(id)
	if index < 0 {
		m.mu.RUnlock()
		return ErrNotFound
	}
	item := m.items[index]
	ruleSink := m.ruleSink
	m.mu.RUnlock()

	etag, lastModified := "", ""
	if conditional {
		etag, lastModified = item.ETag, item.LastModified
	}
	content, metadata, err := m.fetchWithFallback(ctx, item.URL, etag, lastModified)
	if err != nil {
		m.recordRefreshError(id, err)
		return err
	}
	var result ParseResult
	if !metadata.NotModified {
		result, err = m.parser.Parse(content)
		if err != nil {
			m.recordRefreshError(id, err)
			return err
		}
	}

	m.mu.Lock()
	index = m.indexOf(id)
	if index < 0 {
		m.mu.Unlock()
		return ErrNotFound
	}
	previous := m.items[index]
	now := time.Now().UTC()
	if !metadata.NotModified {
		previousSelection := m.items[index].SelectedNodeID
		m.items[index].Nodes = result.Nodes
		m.items[index].SelectedNodeID = ""
		for _, node := range result.Nodes {
			if node.ID == previousSelection {
				m.items[index].SelectedNodeID = previousSelection
				break
			}
		}
		if m.items[index].SelectedNodeID == "" && len(result.Nodes) > 0 {
			m.items[index].SelectedNodeID = result.Nodes[0].ID
		}
	}
	m.items[index].LastUpdated = &now
	m.items[index].LastError = ""
	if metadata.Path != "" {
		m.items[index].LastFetchPath = metadata.Path
	}
	if metadata.ETag != "" {
		m.items[index].ETag = metadata.ETag
	}
	if metadata.LastModified != "" {
		m.items[index].LastModified = metadata.LastModified
	}
	if err := m.persistLocked(); err != nil {
		m.items[index] = previous
		m.mu.Unlock()
		return err
	}
	name := m.items[index].Name
	nodeCount := len(m.items[index].Nodes)
	m.mu.Unlock()
	if !metadata.NotModified && ruleSink != nil {
		if err := ruleSink.SyncSubscriptionRules(id, name, result.ImportedRules); err != nil {
			m.recordRefreshError(id, fmt.Errorf("subscription updated but rules sync failed: %w", err))
			return err
		}
		if err := ruleSink.ReloadRules(); err != nil {
			m.recordRefreshError(id, err)
			return err
		}
	}
	m.publish("subscription.updated", map[string]any{"subscriptionId": id, "nodeCount": nodeCount, "path": metadata.Path})
	return nil
}

func (m *Manager) RunAutoUpdate(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, id := range m.dueForUpdate(now) {
				refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				_ = m.refresh(refreshCtx, id, true)
				cancel()
			}
		}
	}
}

func (m *Manager) dueForUpdate(now time.Time) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var ids []string
	for _, item := range m.items {
		if !item.AutoUpdate {
			continue
		}
		if item.LastUpdated == nil || !item.LastUpdated.Add(time.Duration(item.UpdateIntervalMinutes)*time.Minute).After(now) {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

// fetchWithFallback tries the direct fetch first and, when it fails and a local
// proxy is available, retries once through that proxy. The returned error wraps
// both attempts when both fail.
func (m *Manager) fetchWithFallback(ctx context.Context, rawURL, etag, lastModified string) ([]byte, FetchMetadata, error) {
	content, metadata, directErr := m.fetcher.Fetch(ctx, rawURL, etag, lastModified)
	if directErr == nil {
		return content, metadata, nil
	}
	proxy := m.proxy()
	if proxy == nil {
		return nil, FetchMetadata{}, directErr
	}
	content, metadata, proxyErr := proxy.Fetch(ctx, rawURL, etag, lastModified)
	if proxyErr == nil {
		metadata.Path = "proxy"
		return content, metadata, nil
	}
	return nil, FetchMetadata{}, fmt.Errorf("direct: %v; proxy: %w", directErr, proxyErr)
}

// proxy returns a cached fetcher bound to the current local proxy address, or
// nil when no proxy is configured/running. The fetcher is rebuilt when the
// resolved address changes.
func (m *Manager) proxy() FetchClient {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.proxyResolver == nil {
		return nil
	}
	address := strings.TrimSpace(m.proxyResolver())
	if address == "" {
		m.proxyFetcher, m.proxyAddress = nil, ""
		return nil
	}
	if m.proxyFetcher != nil && m.proxyAddress == address {
		return m.proxyFetcher
	}
	fetcher, err := NewProxyFetcher(address)
	if err != nil {
		return nil
	}
	m.proxyFetcher, m.proxyAddress = fetcher, address
	return fetcher
}

func (m *Manager) recordRefreshError(id string, refreshError error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.indexOf(id)
	if index < 0 {
		return
	}
	m.items[index].LastError = refreshError.Error()
	_ = m.persistLocked()
	m.publish("subscription.failed", map[string]string{"subscriptionId": id, "error": refreshError.Error()})
}

func (m *Manager) load() error {
	content, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		m.items = []Subscription{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read subscriptions: %w", err)
	}
	if err := json.Unmarshal(content, &m.items); err != nil {
		return fmt.Errorf("parse subscriptions: %w", err)
	}
	return nil
}

func (m *Manager) persistLocked() error {
	content, err := json.MarshalIndent(m.items, "", "  ")
	if err != nil {
		return fmt.Errorf("encode subscriptions: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(m.path), ".subscriptions-*.tmp")
	if err != nil {
		return fmt.Errorf("create subscription store: %w", err)
	}
	name := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(name)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, m.path); err != nil {
		return fmt.Errorf("commit subscriptions: %w", err)
	}
	committed = true
	directory, err := os.Open(filepath.Dir(m.path))
	if err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func (m *Manager) indexOf(id string) int {
	for index := range m.items {
		if m.items[index].ID == id {
			return index
		}
	}
	return -1
}

func (m *Manager) publish(eventType string, payload any) {
	if m.events != nil {
		_, _ = m.events.Publish(eventType, payload)
	}
}

func validateInterval(minutes int) error {
	if minutes < minUpdateMinutes || minutes > maxUpdateMinutes {
		return fmt.Errorf("update interval must be between %d and %d minutes", minUpdateMinutes, maxUpdateMinutes)
	}
	return nil
}

func randomID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(value[:])
}
