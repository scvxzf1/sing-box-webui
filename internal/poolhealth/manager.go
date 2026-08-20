package poolhealth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	StatusUnknown     = "unknown"
	StatusHealthy     = "healthy"
	StatusDegraded    = "degraded"
	StatusQuarantined = "quarantined"
	StatusOutage      = "outage"
	maxConcurrent     = 4
	probeTimeout      = 6 * time.Second
	probeRoundTimeout = 30 * time.Second
	fastRetry         = time.Second
	initialBackoff    = 15 * time.Second
)

type Target struct {
	Tag            string `json:"-"`
	SubscriptionID string `json:"subscriptionId"`
	NodeID         string `json:"nodeId"`
	Name           string `json:"name"`
}

type Config struct {
	Address              string
	Secret               string
	ProbeURLs            []string
	Interval             time.Duration
	Tolerance            time.Duration
	IdleTimeout          time.Duration
	HighLatencyThreshold time.Duration
	ConsecutiveFailures  int
	RecoverySuccesses    int
	MaxBackoff           time.Duration
	Targets              []Target
}

type MemberSnapshot struct {
	SubscriptionID string    `json:"subscriptionId"`
	NodeID         string    `json:"nodeId"`
	Name           string    `json:"name"`
	Status         string    `json:"status"`
	LatencyMS      int64     `json:"latencyMs,omitempty"`
	Failures       int       `json:"failures"`
	LastCheckedAt  time.Time `json:"lastCheckedAt,omitempty"`
	NextProbeAt    time.Time `json:"nextProbeAt,omitempty"`
}

type Snapshot struct {
	State          string           `json:"state"`
	SelectedNodeID string           `json:"selectedNodeId,omitempty"`
	SelectedName   string           `json:"selectedName,omitempty"`
	HealthyCount   int              `json:"healthyCount"`
	DegradedCount  int              `json:"degradedCount"`
	Members        []MemberSnapshot `json:"members"`
	LastCheckedAt  time.Time        `json:"lastCheckedAt,omitempty"`
	LastError      string           `json:"lastError,omitempty"`
	Idle           bool             `json:"idle"`
	UploadTotal    int64            `json:"uploadTotal"`
	DownloadTotal  int64            `json:"downloadTotal"`
	Connections    int              `json:"connections"`
	TrafficAt      time.Time        `json:"trafficAt,omitempty"`
}

type memberState struct {
	target            Target
	status            string
	latencyMS         int64
	failures          int
	recoverySuccesses int
	lastCheckedAt     time.Time
	nextProbeAt       time.Time
}

type Manager struct {
	lifecycleMu    sync.Mutex
	mu             sync.RWMutex
	cancel         context.CancelFunc
	done           chan struct{}
	config         Config
	members        map[string]*memberState
	selected       string
	snapshot       Snapshot
	client         *apiClient
	lastActivityAt time.Time
	lastUpload     int64
	lastDownload   int64
	idle           bool
	connections    int
	trafficAt      time.Time
	dormantProbeAt time.Time
}

func NewManager() *Manager {
	return &Manager{snapshot: Snapshot{State: StatusUnknown}}
}

func (m *Manager) Start(config Config) error {
	if err := validateConfig(config); err != nil {
		return err
	}
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.stopLocked()
	ctx, cancel := context.WithCancel(context.Background())
	members := make(map[string]*memberState, len(config.Targets))
	now := time.Now().UTC()
	for _, target := range config.Targets {
		members[target.Tag] = &memberState{target: target, status: StatusUnknown, nextProbeAt: now}
	}
	m.mu.Lock()
	m.cancel = cancel
	m.done = make(chan struct{})
	m.config = config
	m.members = members
	m.selected = ""
	m.client = newAPIClient(config.Address, config.Secret)
	m.lastActivityAt = now
	m.lastUpload, m.lastDownload, m.idle = 0, 0, false
	m.dormantProbeAt = time.Time{}
	m.updateSnapshotLocked("", time.Time{})
	done := m.done
	m.mu.Unlock()
	go m.run(ctx, done)
	return nil
}

func (m *Manager) Stop() {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.stopLocked()
}

func (m *Manager) stopLocked() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done = nil, nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := m.snapshot
	result.Members = append([]MemberSnapshot(nil), result.Members...)
	return result
}

func (m *Manager) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := m.waitReady(readyCtx); err != nil {
		m.setError(err)
		return
	}
	for {
		if m.refreshActivity(ctx) {
			m.checkDormantDue(ctx)
		} else {
			m.checkDue(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.tickInterval()):
		}
	}
}

func (m *Manager) refreshActivity(ctx context.Context) bool {
	m.mu.RLock()
	client := m.client
	m.mu.RUnlock()
	traffic, err := client.traffic(ctx)
	if err != nil {
		return false
	}
	now := time.Now().UTC()
	m.mu.Lock()
	wasIdle := m.idle
	if traffic.UploadTotal != m.lastUpload || traffic.DownloadTotal != m.lastDownload || traffic.ActiveConnections > 0 {
		m.lastActivityAt = now
	}
	m.lastUpload, m.lastDownload = traffic.UploadTotal, traffic.DownloadTotal
	m.connections, m.trafficAt = traffic.ActiveConnections, now
	m.idle = now.Sub(m.lastActivityAt) >= m.config.IdleTimeout
	enteringIdle := !wasIdle && m.idle
	if wasIdle && !m.idle {
		for _, member := range m.members {
			member.nextProbeAt = now
		}
		m.dormantProbeAt = time.Time{}
	} else if enteringIdle {
		m.dormantProbeAt = now.Add(m.config.MaxBackoff)
	}
	m.updateSnapshotLocked("", m.snapshot.LastCheckedAt)
	idle := m.idle
	m.mu.Unlock()
	if enteringIdle {
		if err := client.selectOutbound(ctx, "auto"); err != nil {
			m.setError(fmt.Errorf("return selector to auto while idle: %w", err))
		} else {
			m.mu.Lock()
			m.selected = "auto"
			m.updateSnapshotLocked("", m.snapshot.LastCheckedAt)
			m.mu.Unlock()
		}
	}
	return idle
}

func (m *Manager) waitReady(ctx context.Context) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.mu.RLock()
		client := m.client
		m.mu.RUnlock()
		if err := client.ready(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("health controller unavailable: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (m *Manager) tickInterval() time.Duration {
	m.mu.RLock()
	interval := m.config.Interval
	m.mu.RUnlock()
	if interval > time.Second {
		return time.Second
	}
	return interval
}

type probeResult struct {
	tag       string
	successes int
	total     int
	latencyMS int64
}

func (m *Manager) checkDue(ctx context.Context) {
	m.mu.RLock()
	now := time.Now().UTC()
	due := make([]Target, 0, len(m.members))
	for _, member := range m.members {
		if !member.nextProbeAt.After(now) {
			due = append(due, member.target)
		}
	}
	m.mu.RUnlock()
	m.checkTargets(ctx, due)
}

func (m *Manager) checkDormantDue(ctx context.Context) {
	m.mu.Lock()
	now := time.Now().UTC()
	if m.dormantProbeAt.IsZero() || m.dormantProbeAt.After(now) {
		m.mu.Unlock()
		return
	}
	targets := make([]Target, 0, len(m.members))
	for _, member := range m.members {
		targets = append(targets, member.target)
	}
	m.dormantProbeAt = now.Add(m.config.MaxBackoff)
	m.mu.Unlock()
	m.checkTargets(ctx, targets)
}

func (m *Manager) checkTargets(ctx context.Context, targets []Target) {
	if len(targets) == 0 {
		return
	}
	m.mu.RLock()
	client, probeURLs := m.client, append([]string(nil), m.config.ProbeURLs...)
	m.mu.RUnlock()
	roundCtx, cancel := context.WithTimeout(ctx, probeRoundTimeout)
	defer cancel()

	results := make(chan probeResult, len(targets))
	semaphore := make(chan struct{}, maxConcurrent)
	var wait sync.WaitGroup
	for _, target := range targets {
		target := target
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-roundCtx.Done():
				return
			}
			result := probeTarget(roundCtx, client, target.Tag, probeURLs)
			select {
			case results <- result:
			case <-roundCtx.Done():
			}
		}()
	}
	wait.Wait()
	close(results)

	m.mu.Lock()
	checkedAt := time.Now().UTC()
	for result := range results {
		if member := m.members[result.tag]; member != nil {
			m.applyResultLocked(member, result, checkedAt)
		}
	}
	selection := m.bestSelectionLocked()
	if m.idle && selection != "block" {
		selection = "auto"
	}
	m.mu.Unlock()

	if selection != "" {
		if err := client.selectOutbound(roundCtx, selection); err != nil {
			m.setError(err)
			return
		}
	}
	m.mu.Lock()
	m.selected = selection
	m.updateSnapshotLocked("", checkedAt)
	m.mu.Unlock()
}

func probeTarget(ctx context.Context, client *apiClient, tag string, probeURLs []string) probeResult {
	result := probeResult{tag: tag, total: len(probeURLs)}
	var totalDelay int64
	for _, probeURL := range probeURLs {
		delay, err := client.delay(ctx, tag, probeURL, probeTimeout)
		if err == nil {
			result.successes++
			totalDelay += delay
		}
	}
	if result.successes > 0 {
		result.latencyMS = totalDelay / int64(result.successes)
	}
	return result
}

func (m *Manager) applyResultLocked(member *memberState, result probeResult, now time.Time) {
	member.lastCheckedAt = now
	if result.successes == 0 {
		member.failures++
		member.recoverySuccesses = 0
		if member.failures >= m.config.ConsecutiveFailures {
			member.status = StatusQuarantined
			member.nextProbeAt = now.Add(backoffFor(member.failures-m.config.ConsecutiveFailures, m.config.MaxBackoff))
		} else {
			member.status = StatusDegraded
			member.nextProbeAt = now.Add(fastRetry)
		}
		return
	}

	member.latencyMS = result.latencyMS
	partial := result.successes < result.total || time.Duration(result.latencyMS)*time.Millisecond >= m.config.HighLatencyThreshold
	if member.status == StatusQuarantined {
		member.recoverySuccesses++
		if member.recoverySuccesses < m.config.RecoverySuccesses {
			member.nextProbeAt = now.Add(fastRetry)
			return
		}
	}
	member.failures = 0
	member.recoverySuccesses = 0
	if partial {
		member.status = StatusDegraded
	} else {
		member.status = StatusHealthy
	}
	member.nextProbeAt = now.Add(m.config.Interval)
}

func (m *Manager) bestSelectionLocked() string {
	candidates := make([]*memberState, 0, len(m.members))
	for _, member := range m.members {
		if member.status == StatusHealthy || member.status == StatusDegraded {
			candidates = append(candidates, member)
		}
	}
	if len(candidates) == 0 {
		return "block"
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].status != candidates[j].status {
			return candidates[i].status == StatusHealthy
		}
		left, right := effectiveLatency(candidates[i]), effectiveLatency(candidates[j])
		if left != right {
			return left < right
		}
		return candidates[i].target.Tag < candidates[j].target.Tag
	})
	best := candidates[0]
	current := m.members[m.selected]
	if current == nil || (current.status != StatusHealthy && current.status != StatusDegraded) {
		return best.target.Tag
	}
	if current.status != best.status {
		return best.target.Tag
	}
	currentLatency, bestLatency := effectiveLatency(current), effectiveLatency(best)
	if currentLatency <= bestLatency+int64(m.config.Tolerance/time.Millisecond) {
		return current.target.Tag
	}
	return best.target.Tag
}

func effectiveLatency(member *memberState) int64 {
	if member.latencyMS <= 0 {
		return int64(^uint64(0) >> 1)
	}
	return member.latencyMS
}

func (m *Manager) updateSnapshotLocked(lastError string, checkedAt time.Time) {
	snapshot := Snapshot{
		State: StatusUnknown, LastError: lastError, LastCheckedAt: checkedAt, Idle: m.idle,
		UploadTotal: m.lastUpload, DownloadTotal: m.lastDownload, Connections: m.connections, TrafficAt: m.trafficAt,
	}
	for _, member := range m.members {
		item := MemberSnapshot{
			SubscriptionID: member.target.SubscriptionID, NodeID: member.target.NodeID, Name: member.target.Name,
			Status: member.status, LatencyMS: member.latencyMS, Failures: member.failures,
			LastCheckedAt: member.lastCheckedAt, NextProbeAt: member.nextProbeAt,
		}
		if member.target.Tag == m.selected {
			snapshot.SelectedNodeID, snapshot.SelectedName = member.target.NodeID, member.target.Name
		}
		switch member.status {
		case StatusHealthy:
			snapshot.HealthyCount++
		case StatusDegraded:
			snapshot.DegradedCount++
		}
		snapshot.Members = append(snapshot.Members, item)
	}
	sort.Slice(snapshot.Members, func(i, j int) bool { return snapshot.Members[i].Name < snapshot.Members[j].Name })
	if lastError != "" {
		snapshot.State = StatusDegraded
	} else if snapshot.HealthyCount > 0 {
		snapshot.State = StatusHealthy
	} else if snapshot.DegradedCount > 0 {
		snapshot.State = StatusDegraded
	} else if len(snapshot.Members) > 0 && checkedAt.IsZero() {
		snapshot.State = StatusUnknown
	} else if len(snapshot.Members) > 0 {
		snapshot.State = StatusOutage
	}
	m.snapshot = snapshot
}

func (m *Manager) setError(err error) {
	m.mu.Lock()
	m.updateSnapshotLocked(err.Error(), time.Now().UTC())
	m.mu.Unlock()
}

func backoffFor(exponent int, maximum time.Duration) time.Duration {
	delay := initialBackoff
	for range exponent {
		if delay >= maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func validateConfig(config Config) error {
	if strings.TrimSpace(config.Address) == "" || strings.TrimSpace(config.Secret) == "" {
		return fmt.Errorf("health controller address and secret are required")
	}
	if len(config.ProbeURLs) == 0 || len(config.Targets) < 2 {
		return fmt.Errorf("health monitoring requires probe URLs and at least two targets")
	}
	if config.Interval <= 0 || config.Tolerance < 0 || config.IdleTimeout < config.Interval || config.HighLatencyThreshold <= 0 || config.ConsecutiveFailures < 1 || config.RecoverySuccesses < 1 || config.MaxBackoff < initialBackoff {
		return fmt.Errorf("invalid health monitoring policy")
	}
	return nil
}

type trafficSnapshot struct {
	UploadTotal       int64
	DownloadTotal     int64
	ActiveConnections int
}

type apiClient struct {
	baseURL string
	secret  string
	client  *http.Client
}

func newAPIClient(address, secret string) *apiClient {
	return &apiClient{baseURL: "http://" + address, secret: secret, client: &http.Client{Timeout: probeTimeout + time.Second}}
}

func (c *apiClient) ready(ctx context.Context) error {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/proxies", nil)
	return c.do(request, nil)
}

func (c *apiClient) traffic(ctx context.Context) (trafficSnapshot, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/connections", nil)
	var response struct {
		UploadTotal   int64             `json:"uploadTotal"`
		DownloadTotal int64             `json:"downloadTotal"`
		Connections   []json.RawMessage `json:"connections"`
	}
	if err := c.do(request, &response); err != nil {
		return trafficSnapshot{}, err
	}
	return trafficSnapshot{UploadTotal: response.UploadTotal, DownloadTotal: response.DownloadTotal, ActiveConnections: len(response.Connections)}, nil
}

func (c *apiClient) delay(ctx context.Context, tag, targetURL string, timeout time.Duration) (int64, error) {
	endpoint := c.baseURL + "/proxies/" + url.PathEscape(tag) + "/delay?url=" + url.QueryEscape(targetURL) + "&timeout=" + fmt.Sprint(timeout.Milliseconds())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	var response struct {
		Delay int64 `json:"delay"`
	}
	if err := c.do(request, &response); err != nil {
		return 0, err
	}
	if response.Delay <= 0 {
		return 0, fmt.Errorf("invalid delay response")
	}
	return response.Delay, nil
}

func (c *apiClient) selectOutbound(ctx context.Context, tag string) error {
	body, _ := json.Marshal(map[string]string{"name": tag})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/proxies/proxy", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return c.do(request, nil)
}

func (c *apiClient) do(request *http.Request, output any) error {
	request.Header.Set("Authorization", "Bearer "+c.secret)
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("health controller returned HTTP %d", response.StatusCode)
	}
	if output != nil {
		if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(output); err != nil {
			return err
		}
	}
	return nil
}
