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
	MaxImportLinks       = 200
	maxImportTextBytes   = 256 << 10
)

var ErrNotFound = errors.New("subscription not found")
var ErrNodeNotFound = errors.New("node not found")

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

type ImportNodesInput struct {
	Links string `json:"links"`
}

type ImportNodeItem struct {
	Line   int       `json:"line"`
	Status string    `json:"status"`
	Error  string    `json:"error,omitempty"`
	Node   *NodeView `json:"node,omitempty"`
}

type ImportNodesResult struct {
	AddedCount     int              `json:"addedCount"`
	DuplicateCount int              `json:"duplicateCount"`
	InvalidCount   int              `json:"invalidCount"`
	Items          []ImportNodeItem `json:"items"`
	Subscription   View             `json:"subscription"`
}

type RuleSink interface {
	SyncSubscriptionRules(subscriptionID, subscriptionName string, rules []ImportedRule) error
	DeleteSubscriptionRules(subscriptionID string) error
	ReloadRules() error
}

type PoolSink interface {
	ReconcileSubscriptionNodes(subscriptionID string, previous, current []Node) error
	DeleteSubscriptionMembers(subscriptionID string) error
}

type Manager struct {
	mu           sync.RWMutex
	refreshMu    sync.Mutex
	refreshLocks map[string]*refreshLockEntry
	path         string
	items        []Subscription
	fetcher      FetchClient
	parser       Parser
	events       *events.Broker
	ruleSink     RuleSink
	poolSink     PoolSink

	proxyResolver func() string
	proxyFetcher  FetchClient
	proxyAddress  string
}

type refreshLockEntry struct {
	mu   sync.Mutex
	refs int
}

func (m *Manager) SetRuleSink(sink RuleSink) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ruleSink = sink
}

func (m *Manager) SetPoolSink(sink PoolSink) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.poolSink = sink
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

func (m *Manager) NodeLink(subscriptionID, nodeID string) (NodeLink, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	index := m.indexOf(subscriptionID)
	if index < 0 {
		return NodeLink{}, ErrNotFound
	}
	for _, node := range m.items[index].Nodes {
		if node.ID != nodeID {
			continue
		}
		if node.OriginalLink != "" {
			return NodeLink{Link: node.OriginalLink, Source: NodeLinkSourceOriginal}, nil
		}
		link, err := EncodeNodeLink(node)
		if err != nil {
			return NodeLink{}, err
		}
		return NodeLink{Link: link, Source: NodeLinkSourceGenerated}, nil
	}
	return NodeLink{}, ErrNodeNotFound
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
		rollbackErr := m.Delete(item.ID)
		if rollbackErr != nil && !errors.Is(rollbackErr, ErrNotFound) {
			return View{}, errors.Join(err, fmt.Errorf("rollback failed: %w", rollbackErr))
		}
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

func (m *Manager) ImportNodes(id string, input ImportNodesInput) (ImportNodesResult, error) {
	if len(input.Links) > maxImportTextBytes {
		return ImportNodesResult{}, fmt.Errorf("node link input is too large")
	}
	type parsedItem struct {
		line int
		node Node
		err  error
	}
	parsed := make([]parsedItem, 0)
	normalized := strings.ReplaceAll(input.Links, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	for lineIndex, raw := range strings.Split(normalized, "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if len(parsed) >= MaxImportLinks {
			return ImportNodesResult{}, fmt.Errorf("a maximum of %d node links can be imported at once", MaxImportLinks)
		}
		node, err := ParseNodeLink(raw)
		parsed = append(parsed, parsedItem{line: lineIndex + 1, node: node, err: err})
	}
	if len(parsed) == 0 {
		return ImportNodesResult{}, fmt.Errorf("at least one node link is required")
	}

	release, err := m.acquireRefreshLock(id)
	if err != nil {
		return ImportNodesResult{}, err
	}
	defer release()

	m.mu.Lock()
	index := m.indexOf(id)
	if index < 0 {
		m.mu.Unlock()
		return ImportNodesResult{}, ErrNotFound
	}
	previousNodes := append([]Node(nil), m.items[index].Nodes...)
	previousManualIDs := append([]string(nil), m.items[index].ManualNodeIDs...)
	previousSelection := m.items[index].SelectedNodeID
	existing := make(map[string]Node, len(previousNodes))
	for _, node := range previousNodes {
		existing[node.ID] = node
	}
	manualIDs := make(map[string]struct{}, len(previousManualIDs))
	for _, nodeID := range previousManualIDs {
		manualIDs[nodeID] = struct{}{}
	}

	result := ImportNodesResult{Items: make([]ImportNodeItem, 0, len(parsed))}
	changed := false
	for _, item := range parsed {
		if item.err != nil {
			result.InvalidCount++
			result.Items = append(result.Items, ImportNodeItem{Line: item.line, Status: "invalid", Error: item.err.Error()})
			continue
		}
		if _, duplicate := existing[item.node.ID]; duplicate {
			result.DuplicateCount++
			for nodeIndex := range m.items[index].Nodes {
				if m.items[index].Nodes[nodeIndex].ID == item.node.ID {
					m.items[index].Nodes[nodeIndex] = item.node
					break
				}
			}
			existing[item.node.ID] = item.node
			view := toNodeView(item.node, m.items[index].SelectedNodeID)
			result.Items = append(result.Items, ImportNodeItem{Line: item.line, Status: "duplicate", Node: &view})
			if _, tracked := manualIDs[item.node.ID]; !tracked {
				m.items[index].ManualNodeIDs = append(m.items[index].ManualNodeIDs, item.node.ID)
				manualIDs[item.node.ID] = struct{}{}
			}
			changed = true
			continue
		}
		m.items[index].Nodes = append(m.items[index].Nodes, item.node)
		m.items[index].ManualNodeIDs = append(m.items[index].ManualNodeIDs, item.node.ID)
		existing[item.node.ID] = item.node
		manualIDs[item.node.ID] = struct{}{}
		if m.items[index].SelectedNodeID == "" {
			m.items[index].SelectedNodeID = item.node.ID
		}
		view := toNodeView(item.node, m.items[index].SelectedNodeID)
		result.Items = append(result.Items, ImportNodeItem{Line: item.line, Status: "added", Node: &view})
		result.AddedCount++
		changed = true
	}
	if changed {
		if err := m.persistLocked(); err != nil {
			m.items[index].Nodes = previousNodes
			m.items[index].ManualNodeIDs = previousManualIDs
			m.items[index].SelectedNodeID = previousSelection
			m.mu.Unlock()
			return ImportNodesResult{}, err
		}
	}
	result.Subscription = toView(m.items[index], true)
	m.mu.Unlock()
	if changed {
		m.publish("nodes.imported", map[string]any{"subscriptionId": id, "addedCount": result.AddedCount, "duplicateCount": result.DuplicateCount})
	}
	return result, nil
}

func (m *Manager) Delete(id string) error {
	release, err := m.acquireRefreshLock(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			if cleanupErr := m.cleanupDependencies(id); cleanupErr != nil {
				return fmt.Errorf("subscription not found and dependent cleanup failed: %w", cleanupErr)
			}
		}
		return err
	}
	defer release()
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
	m.mu.Unlock()
	m.publish("subscription.deleted", map[string]string{"subscriptionId": id})
	if err := m.cleanupDependencies(id); err != nil {
		return fmt.Errorf("subscription deleted but dependent cleanup failed: %w", err)
	}
	return nil
}

func (m *Manager) cleanupDependencies(id string) error {
	m.mu.RLock()
	sink := m.ruleSink
	poolSink := m.poolSink
	m.mu.RUnlock()
	var cleanupErrors []error
	if poolSink != nil {
		if err := poolSink.DeleteSubscriptionMembers(id); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("node pool cleanup failed: %w", err))
		}
	}
	if sink != nil {
		if err := sink.DeleteSubscriptionRules(id); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("rules cleanup failed: %w", err))
		} else if err := sink.ReloadRules(); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("rules reload failed: %w", err))
		}
	}
	if err := errors.Join(cleanupErrors...); err != nil {
		return err
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
	// Serialize refreshes and deletion, and reclaim idle per-ID entries.
	release, err := m.acquireRefreshLock(id)
	if err != nil {
		return err
	}
	defer release()

	m.mu.RLock()
	index := m.indexOf(id)
	if index < 0 {
		m.mu.RUnlock()
		return ErrNotFound
	}
	item := m.items[index]
	ruleSink := m.ruleSink
	poolSink := m.poolSink
	m.mu.RUnlock()

	etag, lastModified := "", ""
	if conditional {
		etag, lastModified = item.ETag, item.LastModified
	}
	content, metadata, err := m.fetchWithFallback(ctx, item.URL, etag, lastModified)
	if err != nil {
		return m.recordRefreshError(id, err)
	}
	var result ParseResult
	if !metadata.NotModified {
		result, err = m.parser.Parse(content)
		if err != nil {
			return m.recordRefreshError(id, err)
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
		manualNodes := nodesByID(previous.Nodes, previous.ManualNodeIDs)
		m.items[index].Nodes = mergeNodeLists(result.Nodes, manualNodes)
		m.items[index].SelectedNodeID = ""
		for _, node := range m.items[index].Nodes {
			if node.ID == previousSelection {
				m.items[index].SelectedNodeID = previousSelection
				break
			}
		}
		if m.items[index].SelectedNodeID == "" && len(m.items[index].Nodes) > 0 {
			m.items[index].SelectedNodeID = m.items[index].Nodes[0].ID
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
	currentNodes := append([]Node(nil), m.items[index].Nodes...)
	m.mu.Unlock()
	if poolSink != nil {
		if err := poolSink.ReconcileSubscriptionNodes(id, previous.Nodes, currentNodes); err != nil {
			return m.recordRefreshError(id, fmt.Errorf("subscription updated but node pool reconciliation failed: %w", err))
		}
	}
	if !metadata.NotModified && ruleSink != nil {
		if err := ruleSink.SyncSubscriptionRules(id, name, result.ImportedRules); err != nil {
			return m.recordRefreshError(id, fmt.Errorf("subscription updated but rules sync failed: %w", err))
		}
		if err := ruleSink.ReloadRules(); err != nil {
			return m.recordRefreshError(id, err)
		}
	}
	m.publish("subscription.updated", map[string]any{"subscriptionId": id, "nodeCount": nodeCount, "path": metadata.Path})
	return nil
}

func (m *Manager) acquireRefreshLock(id string) (func(), error) {
	m.mu.RLock()
	exists := m.indexOf(id) >= 0
	m.mu.RUnlock()
	if !exists {
		return nil, ErrNotFound
	}
	m.refreshMu.Lock()
	if m.refreshLocks == nil {
		m.refreshLocks = make(map[string]*refreshLockEntry)
	}
	entry := m.refreshLocks[id]
	if entry == nil {
		entry = &refreshLockEntry{}
		m.refreshLocks[id] = entry
	}
	entry.refs++
	m.refreshMu.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		m.refreshMu.Lock()
		entry.refs--
		if entry.refs == 0 && m.refreshLocks[id] == entry {
			delete(m.refreshLocks, id)
		}
		m.refreshMu.Unlock()
	}, nil
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

func (m *Manager) recordRefreshError(id string, refreshError error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.indexOf(id)
	if index < 0 {
		return refreshError
	}
	refreshError = redactError(refreshError, m.items[index].URL)
	message := refreshError.Error()
	m.items[index].LastError = message
	_ = m.persistLocked()
	m.publish("subscription.failed", map[string]string{"subscriptionId": id, "error": message})
	return refreshError
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
	changed := false
	for index := range m.items {
		redacted := redactURLInText(m.items[index].LastError, m.items[index].URL)
		if redacted != m.items[index].LastError {
			m.items[index].LastError = redacted
			changed = true
		}
	}
	if changed {
		return m.persistLocked()
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

func nodesByID(nodes []Node, ids []string) []Node {
	byID := make(map[string]Node, len(nodes))
	for _, node := range nodes {
		byID[node.ID] = node
	}
	result := make([]Node, 0, len(ids))
	for _, id := range ids {
		if node, ok := byID[id]; ok {
			result = append(result, node)
		}
	}
	return result
}

func mergeNodeLists(primary, additional []Node) []Node {
	result := make([]Node, 0, len(primary)+len(additional))
	seen := make(map[string]struct{}, len(primary)+len(additional))
	for _, nodes := range [][]Node{primary, additional} {
		for _, node := range nodes {
			if _, exists := seen[node.ID]; exists {
				continue
			}
			seen[node.ID] = struct{}{}
			result = append(result, node)
		}
	}
	return result
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
