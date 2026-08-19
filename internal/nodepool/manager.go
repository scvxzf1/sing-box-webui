package nodepool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sing-box-webui/internal/netresolve"
	"sing-box-webui/internal/netsafety"
	"sing-box-webui/internal/subscription"
)

const (
	defaultProbeIntervalSeconds = 60
	defaultToleranceMS          = 80
	defaultProbeURL             = "https://cp.cloudflare.com/generate_204"
	defaultIdleTimeoutSeconds   = 30 * 60
	defaultHighLatencyMS        = 3000
	defaultConsecutiveFailures  = 2
	defaultRecoverySuccesses    = 2
	defaultMaxBackoffSeconds    = 5 * 60
	maxFallbackProbeURLs        = 4
	maxMembers                  = 128
)

var ErrNotFound = errors.New("node pool not found")

type Member struct {
	SubscriptionID string `json:"subscriptionId"`
	NodeID         string `json:"nodeId"`
}

type Pool struct {
	ID                           string    `json:"id"`
	Name                         string    `json:"name"`
	Members                      []Member  `json:"members"`
	ProbeIntervalSeconds         int       `json:"probeIntervalSeconds"`
	ToleranceMS                  int       `json:"toleranceMs"`
	ProbeURL                     string    `json:"probeUrl"`
	FallbackProbeURLs            []string  `json:"fallbackProbeUrls,omitempty"`
	IdleTimeoutSeconds           int       `json:"idleTimeoutSeconds"`
	HighLatencyThresholdMS       int       `json:"highLatencyThresholdMs"`
	ConsecutiveFailures          int       `json:"consecutiveFailures"`
	RecoverySuccesses            int       `json:"recoverySuccesses"`
	MaxBackoffSeconds            int       `json:"maxBackoffSeconds"`
	InterruptExistingConnections bool      `json:"interruptExistingConnections"`
	CreatedAt                    time.Time `json:"createdAt"`
	UpdatedAt                    time.Time `json:"updatedAt"`
}

type CreateInput struct {
	Name                         string   `json:"name"`
	Members                      []Member `json:"members"`
	ProbeIntervalSeconds         int      `json:"probeIntervalSeconds"`
	ToleranceMS                  int      `json:"toleranceMs"`
	ProbeURL                     string   `json:"probeUrl"`
	FallbackProbeURLs            []string `json:"fallbackProbeUrls"`
	IdleTimeoutSeconds           int      `json:"idleTimeoutSeconds"`
	HighLatencyThresholdMS       int      `json:"highLatencyThresholdMs"`
	ConsecutiveFailures          int      `json:"consecutiveFailures"`
	RecoverySuccesses            int      `json:"recoverySuccesses"`
	MaxBackoffSeconds            int      `json:"maxBackoffSeconds"`
	InterruptExistingConnections bool     `json:"interruptExistingConnections"`
}

type UpdateInput struct {
	Name                         *string   `json:"name,omitempty"`
	Members                      *[]Member `json:"members,omitempty"`
	ProbeIntervalSeconds         *int      `json:"probeIntervalSeconds,omitempty"`
	ToleranceMS                  *int      `json:"toleranceMs,omitempty"`
	ProbeURL                     *string   `json:"probeUrl,omitempty"`
	FallbackProbeURLs            *[]string `json:"fallbackProbeUrls,omitempty"`
	IdleTimeoutSeconds           *int      `json:"idleTimeoutSeconds,omitempty"`
	HighLatencyThresholdMS       *int      `json:"highLatencyThresholdMs,omitempty"`
	ConsecutiveFailures          *int      `json:"consecutiveFailures,omitempty"`
	RecoverySuccesses            *int      `json:"recoverySuccesses,omitempty"`
	MaxBackoffSeconds            *int      `json:"maxBackoffSeconds,omitempty"`
	InterruptExistingConnections *bool     `json:"interruptExistingConnections,omitempty"`
}

type MemberView struct {
	SubscriptionID   string `json:"subscriptionId"`
	SubscriptionName string `json:"subscriptionName,omitempty"`
	NodeID           string `json:"nodeId"`
	NodeName         string `json:"nodeName,omitempty"`
	Type             string `json:"type,omitempty"`
	Server           string `json:"server,omitempty"`
	Port             uint16 `json:"port,omitempty"`
	Available        bool   `json:"available"`
}

type View struct {
	ID                           string       `json:"id"`
	Name                         string       `json:"name"`
	Members                      []MemberView `json:"members"`
	MemberCount                  int          `json:"memberCount"`
	AvailableCount               int          `json:"availableCount"`
	ProbeIntervalSeconds         int          `json:"probeIntervalSeconds"`
	ToleranceMS                  int          `json:"toleranceMs"`
	ProbeURL                     string       `json:"probeUrl"`
	FallbackProbeURLs            []string     `json:"fallbackProbeUrls"`
	IdleTimeoutSeconds           int          `json:"idleTimeoutSeconds"`
	HighLatencyThresholdMS       int          `json:"highLatencyThresholdMs"`
	ConsecutiveFailures          int          `json:"consecutiveFailures"`
	RecoverySuccesses            int          `json:"recoverySuccesses"`
	MaxBackoffSeconds            int          `json:"maxBackoffSeconds"`
	InterruptExistingConnections bool         `json:"interruptExistingConnections"`
	CreatedAt                    time.Time    `json:"createdAt"`
	UpdatedAt                    time.Time    `json:"updatedAt"`
}

type Manager struct {
	mu            sync.RWMutex
	path          string
	items         []Pool
	subscriptions *subscription.Manager
}

func OpenManager(dataDirectory string, subscriptions *subscription.Manager) (*Manager, error) {
	if subscriptions == nil {
		return nil, fmt.Errorf("subscriptions manager is required")
	}
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create node pool directory: %w", err)
	}
	if err := os.Chmod(dataDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("set node pool directory permissions: %w", err)
	}
	manager := &Manager{path: filepath.Join(dataDirectory, "pools.json"), subscriptions: subscriptions}
	if err := manager.load(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) List() []View {
	m.mu.RLock()
	items := append([]Pool(nil), m.items...)
	m.mu.RUnlock()
	views := make([]View, 0, len(items))
	for _, item := range items {
		views = append(views, m.toView(item))
	}
	return views
}

func (m *Manager) Reorder(ids []string) ([]View, error) {
	m.mu.Lock()
	if len(ids) != len(m.items) {
		m.mu.Unlock()
		return nil, fmt.Errorf("order must include every node pool exactly once")
	}
	indices := make(map[string]int, len(m.items))
	for index, item := range m.items {
		indices[item.ID] = index
	}
	seen := make(map[string]struct{}, len(ids))
	ordered := make([]Pool, len(ids))
	for position, id := range ids {
		index, exists := indices[id]
		if !exists {
			m.mu.Unlock()
			return nil, fmt.Errorf("order contains an unknown node pool")
		}
		if _, duplicate := seen[id]; duplicate {
			m.mu.Unlock()
			return nil, fmt.Errorf("order contains a duplicate node pool")
		}
		seen[id] = struct{}{}
		ordered[position] = m.items[index]
	}
	previous := m.items
	m.items = ordered
	if err := m.persistLocked(); err != nil {
		m.items = previous
		m.mu.Unlock()
		return nil, err
	}
	views := make([]View, 0, len(m.items))
	for _, item := range m.items {
		views = append(views, m.toView(item))
	}
	m.mu.Unlock()
	return views, nil
}

func (m *Manager) Get(id string) (View, error) {
	pool, err := m.get(id)
	if err != nil {
		return View{}, err
	}
	return m.toView(pool), nil
}

func (m *Manager) Create(input CreateInput) (View, error) {
	now := time.Now().UTC()
	pool, err := validatePool(Pool{
		ID: randomID(), Name: input.Name, Members: input.Members,
		ProbeIntervalSeconds: input.ProbeIntervalSeconds, ToleranceMS: input.ToleranceMS,
		ProbeURL: input.ProbeURL, FallbackProbeURLs: input.FallbackProbeURLs, IdleTimeoutSeconds: input.IdleTimeoutSeconds,
		HighLatencyThresholdMS: input.HighLatencyThresholdMS, ConsecutiveFailures: input.ConsecutiveFailures,
		RecoverySuccesses: input.RecoverySuccesses, MaxBackoffSeconds: input.MaxBackoffSeconds,
		InterruptExistingConnections: input.InterruptExistingConnections, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return View{}, err
	}
	if err := m.validateMembers(pool.Members); err != nil {
		return View{}, err
	}
	m.mu.Lock()
	m.items = append(m.items, pool)
	if err := m.persistLocked(); err != nil {
		m.items = m.items[:len(m.items)-1]
		m.mu.Unlock()
		return View{}, err
	}
	m.mu.Unlock()
	return m.toView(pool), nil
}

func (m *Manager) Update(id string, input UpdateInput) (View, error) {
	m.mu.Lock()
	index := m.indexOf(id)
	if index < 0 {
		m.mu.Unlock()
		return View{}, ErrNotFound
	}
	updated := m.items[index]
	if input.Name != nil {
		updated.Name = *input.Name
	}
	if input.Members != nil {
		updated.Members = *input.Members
	}
	if input.ProbeIntervalSeconds != nil {
		updated.ProbeIntervalSeconds = *input.ProbeIntervalSeconds
	}
	if input.ToleranceMS != nil {
		updated.ToleranceMS = *input.ToleranceMS
	}
	if input.ProbeURL != nil {
		updated.ProbeURL = *input.ProbeURL
	}
	if input.FallbackProbeURLs != nil {
		updated.FallbackProbeURLs = *input.FallbackProbeURLs
	}
	if input.IdleTimeoutSeconds != nil {
		updated.IdleTimeoutSeconds = *input.IdleTimeoutSeconds
	}
	if input.HighLatencyThresholdMS != nil {
		updated.HighLatencyThresholdMS = *input.HighLatencyThresholdMS
	}
	if input.ConsecutiveFailures != nil {
		updated.ConsecutiveFailures = *input.ConsecutiveFailures
	}
	if input.RecoverySuccesses != nil {
		updated.RecoverySuccesses = *input.RecoverySuccesses
	}
	if input.MaxBackoffSeconds != nil {
		updated.MaxBackoffSeconds = *input.MaxBackoffSeconds
	}
	if input.InterruptExistingConnections != nil {
		updated.InterruptExistingConnections = *input.InterruptExistingConnections
	}
	updated, err := validatePool(updated)
	if err != nil {
		m.mu.Unlock()
		return View{}, err
	}
	if err := m.validateMembers(updated.Members); err != nil {
		m.mu.Unlock()
		return View{}, err
	}
	updated.UpdatedAt = time.Now().UTC()
	previous := m.items[index]
	m.items[index] = updated
	if err := m.persistLocked(); err != nil {
		m.items[index] = previous
		m.mu.Unlock()
		return View{}, err
	}
	m.mu.Unlock()
	return m.toView(updated), nil
}

func (m *Manager) validateMembers(members []Member) error {
	for _, member := range members {
		if _, _, err := m.subscriptions.SelectedNode(member.SubscriptionID, member.NodeID); err != nil {
			return fmt.Errorf("node pool member %s/%s is unavailable: %w", member.SubscriptionID, member.NodeID, err)
		}
	}
	return nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.indexOf(id)
	if index < 0 {
		return ErrNotFound
	}
	previous := append([]Pool(nil), m.items...)
	m.items = append(m.items[:index], m.items[index+1:]...)
	if err := m.persistLocked(); err != nil {
		m.items = previous
		return err
	}
	return nil
}

func (m *Manager) Resolve(id string) (Pool, []subscription.Node, error) {
	pool, _, nodes, err := m.ResolveWithMembers(id)
	return pool, nodes, err
}

func (m *Manager) ResolveWithMembers(id string) (Pool, []Member, []subscription.Node, error) {
	pool, err := m.get(id)
	if err != nil {
		return Pool{}, nil, nil, err
	}
	resolvedMembers := make([]Member, 0, len(pool.Members))
	nodes := make([]subscription.Node, 0, len(pool.Members))
	for _, member := range pool.Members {
		_, node, resolveErr := m.subscriptions.SelectedNode(member.SubscriptionID, member.NodeID)
		if resolveErr == nil {
			resolvedMembers = append(resolvedMembers, member)
			nodes = append(nodes, node)
		}
	}
	if len(nodes) < 2 {
		return pool, nil, nil, fmt.Errorf("node pool requires at least 2 available members")
	}
	return pool, resolvedMembers, nodes, nil
}

// ValidateProbeURLs resolves each probe host through the controlled resolver
// and rejects any result outside the public address space before a core apply.
// The core still owns the final connection, so this is a validation guard
// against stale or obviously unsafe DNS answers rather than an address pin.
func (m *Manager) ValidateProbeURLs(ctx context.Context, id string) error {
	pool, err := m.get(id)
	if err != nil {
		return err
	}
	urls := append([]string{pool.ProbeURL}, pool.FallbackProbeURLs...)
	for _, raw := range urls {
		parsed, parseErr := url.ParseRequestURI(raw)
		if parseErr != nil || parsed.Hostname() == "" {
			return fmt.Errorf("probe URL must be a valid HTTPS URL")
		}
		addresses, resolveErr := netresolve.PublicAddresses(ctx, parsed.Hostname())
		if resolveErr != nil {
			return fmt.Errorf("resolve probe URL host: %w", resolveErr)
		}
		for _, address := range addresses {
			if !netsafety.AllowedPublicAddress(address) {
				return fmt.Errorf("probe URL host resolves to a non-public address")
			}
		}
	}
	return nil
}

func (m *Manager) get(id string) (Pool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	index := m.indexOf(id)
	if index < 0 {
		return Pool{}, ErrNotFound
	}
	pool := m.items[index]
	pool.Members = append([]Member(nil), pool.Members...)
	pool.FallbackProbeURLs = append([]string(nil), pool.FallbackProbeURLs...)
	return pool, nil
}

func (m *Manager) toView(pool Pool) View {
	view := View{
		ID: pool.ID, Name: pool.Name, Members: make([]MemberView, 0, len(pool.Members)), MemberCount: len(pool.Members),
		ProbeIntervalSeconds: pool.ProbeIntervalSeconds, ToleranceMS: pool.ToleranceMS,
		ProbeURL: pool.ProbeURL, FallbackProbeURLs: append([]string{}, pool.FallbackProbeURLs...), IdleTimeoutSeconds: pool.IdleTimeoutSeconds,
		HighLatencyThresholdMS: pool.HighLatencyThresholdMS, ConsecutiveFailures: pool.ConsecutiveFailures,
		RecoverySuccesses: pool.RecoverySuccesses, MaxBackoffSeconds: pool.MaxBackoffSeconds,
		InterruptExistingConnections: pool.InterruptExistingConnections,
		CreatedAt:                    pool.CreatedAt, UpdatedAt: pool.UpdatedAt,
	}
	for _, member := range pool.Members {
		memberView := MemberView{SubscriptionID: member.SubscriptionID, NodeID: member.NodeID}
		if subscriptionView, err := m.subscriptions.Get(member.SubscriptionID); err == nil {
			memberView.SubscriptionName = subscriptionView.Name
			for _, node := range subscriptionView.Nodes {
				if node.ID == member.NodeID {
					memberView.NodeName, memberView.Type = node.Name, node.Type
					memberView.Server, memberView.Port, memberView.Available = node.Server, node.Port, true
					view.AvailableCount++
					break
				}
			}
		}
		view.Members = append(view.Members, memberView)
	}
	return view
}

func (m *Manager) load() error {
	content, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		m.items = []Pool{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read node pools: %w", err)
	}
	if err := json.Unmarshal(content, &m.items); err != nil {
		return fmt.Errorf("parse node pools: %w", err)
	}
	for index := range m.items {
		normalizePoolDefaults(&m.items[index])
	}
	return nil
}

func (m *Manager) persistLocked() error {
	content, err := json.MarshalIndent(m.items, "", "  ")
	if err != nil {
		return fmt.Errorf("encode node pools: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(m.path), ".pools-*.tmp")
	if err != nil {
		return fmt.Errorf("create node pool store: %w", err)
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
		return fmt.Errorf("commit node pools: %w", err)
	}
	committed = true
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

func validate(name string, members []Member, interval, tolerance int) (string, []Member, int, int, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return "", nil, 0, 0, fmt.Errorf("node pool name must contain 1-80 characters")
	}
	if len(members) > maxMembers {
		return "", nil, 0, 0, fmt.Errorf("node pool cannot contain more than %d members", maxMembers)
	}
	if interval == 0 {
		interval = defaultProbeIntervalSeconds
	}
	if interval < 15 || interval > 3600 {
		return "", nil, 0, 0, fmt.Errorf("probe interval must be between 15 and 3600 seconds")
	}
	if tolerance == 0 {
		tolerance = defaultToleranceMS
	}
	if tolerance < 0 || tolerance > 1000 {
		return "", nil, 0, 0, fmt.Errorf("tolerance must be between 0 and 1000 milliseconds")
	}
	clean := make([]Member, 0, len(members))
	seen := make(map[string]struct{}, len(members))
	for _, member := range members {
		member.SubscriptionID = strings.TrimSpace(member.SubscriptionID)
		member.NodeID = strings.TrimSpace(member.NodeID)
		if member.SubscriptionID == "" || member.NodeID == "" {
			return "", nil, 0, 0, fmt.Errorf("node pool member requires subscriptionId and nodeId")
		}
		key := member.SubscriptionID + "\x00" + member.NodeID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		clean = append(clean, member)
	}
	return name, clean, interval, tolerance, nil
}

func validatePool(pool Pool) (Pool, error) {
	name, members, interval, tolerance, err := validate(pool.Name, pool.Members, pool.ProbeIntervalSeconds, pool.ToleranceMS)
	if err != nil {
		return Pool{}, err
	}
	pool.Name, pool.Members = name, members
	pool.ProbeIntervalSeconds, pool.ToleranceMS = interval, tolerance
	normalizePoolDefaults(&pool)
	parsedURL, err := url.ParseRequestURI(pool.ProbeURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || len(pool.ProbeURL) > 2048 || blockedProbeHost(parsedURL.Hostname()) {
		return Pool{}, fmt.Errorf("probe URL must be a valid HTTPS URL")
	}
	if pool.IdleTimeoutSeconds < 60 || pool.IdleTimeoutSeconds > 24*60*60 {
		return Pool{}, fmt.Errorf("idle timeout must be between 60 and 86400 seconds")
	}
	if pool.ProbeIntervalSeconds > pool.IdleTimeoutSeconds {
		return Pool{}, fmt.Errorf("probe interval cannot exceed idle timeout")
	}
	if len(pool.FallbackProbeURLs) > maxFallbackProbeURLs {
		return Pool{}, fmt.Errorf("a maximum of %d fallback probe URLs is allowed", maxFallbackProbeURLs)
	}
	seenURLs := map[string]struct{}{pool.ProbeURL: {}}
	cleanURLs := make([]string, 0, len(pool.FallbackProbeURLs))
	for _, value := range pool.FallbackProbeURLs {
		value = strings.TrimSpace(value)
		parsedFallback, parseErr := url.ParseRequestURI(value)
		if parseErr != nil || parsedFallback.Scheme != "https" || parsedFallback.Host == "" || len(value) > 2048 || blockedProbeHost(parsedFallback.Hostname()) {
			return Pool{}, fmt.Errorf("fallback probe URLs must be valid HTTPS URLs")
		}
		if _, exists := seenURLs[value]; exists {
			continue
		}
		seenURLs[value] = struct{}{}
		cleanURLs = append(cleanURLs, value)
	}
	pool.FallbackProbeURLs = cleanURLs
	if pool.HighLatencyThresholdMS < 100 || pool.HighLatencyThresholdMS > 10000 {
		return Pool{}, fmt.Errorf("high latency threshold must be between 100 and 10000 milliseconds")
	}
	if pool.ConsecutiveFailures < 1 || pool.ConsecutiveFailures > 10 {
		return Pool{}, fmt.Errorf("consecutive failures must be between 1 and 10")
	}
	if pool.RecoverySuccesses < 1 || pool.RecoverySuccesses > 10 {
		return Pool{}, fmt.Errorf("recovery successes must be between 1 and 10")
	}
	if pool.MaxBackoffSeconds < 15 || pool.MaxBackoffSeconds > 3600 {
		return Pool{}, fmt.Errorf("maximum backoff must be between 15 and 3600 seconds")
	}
	return pool, nil
}

func blockedProbeHost(host string) bool {
	address, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err == nil && !netsafety.AllowedPublicAddress(address)
}

func normalizePoolDefaults(pool *Pool) {
	if pool.FallbackProbeURLs == nil {
		pool.FallbackProbeURLs = []string{}
	}
	pool.ProbeURL = strings.TrimSpace(pool.ProbeURL)
	if pool.ProbeURL == "" {
		pool.ProbeURL = defaultProbeURL
	}
	if pool.IdleTimeoutSeconds == 0 {
		pool.IdleTimeoutSeconds = defaultIdleTimeoutSeconds
	}
	if pool.HighLatencyThresholdMS == 0 {
		pool.HighLatencyThresholdMS = defaultHighLatencyMS
	}
	if pool.ConsecutiveFailures == 0 {
		pool.ConsecutiveFailures = defaultConsecutiveFailures
	}
	if pool.RecoverySuccesses == 0 {
		pool.RecoverySuccesses = defaultRecoverySuccesses
	}
	if pool.MaxBackoffSeconds == 0 {
		pool.MaxBackoffSeconds = defaultMaxBackoffSeconds
	}
}

func randomID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(value[:])
}
