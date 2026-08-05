// Package connmon watches the sing-box Clash API for live connections and
// keeps a bounded, in-memory cache of recent links so the control plane can
// render which upstream node carried each proxied flow, how much it moved,
// and at what rate.
package connmon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// MaxLinks bounds the in-memory link cache.
	MaxLinks     = 1000
	pollInterval = time.Second
	readyTimeout = 8 * time.Second
	httpTimeout  = 4 * time.Second
)

// Resolver maps a chain (proxy group / member tags) to a human-readable node name.
type Resolver func(chains []string) string

// Link is a single observed connection.
type Link struct {
	ID            string    `json:"id"`
	Host          string    `json:"host"`
	Network       string    `json:"network"`
	Type          string    `json:"type"`
	Upload        int64     `json:"upload"`
	Download      int64     `json:"download"`
	UploadRate    float64   `json:"uploadRate"`
	DownloadRate  float64   `json:"downloadRate"`
	Node          string    `json:"node"`
	Chain         []string  `json:"chain,omitempty"`
	StartedAt     time.Time `json:"startedAt"`
	FirstSeenAt   time.Time `json:"firstSeenAt"`
	LastSeenAt    time.Time `json:"lastSeenAt"`
	Active        bool      `json:"active"`
	ClosedCounted bool      `json:"-"`
}

// Stats summarises the current snapshot.
type Stats struct {
	Active          int   `json:"active"`
	Total           int   `json:"total"`
	UploadTotal     int64 `json:"uploadTotal"`
	DownloadTotal   int64 `json:"downloadTotal"`
	UploadRate      int64 `json:"uploadRate"`
	DownloadRate    int64 `json:"downloadRate"`
	TrackedCapacity int   `json:"trackedCapacity"`
}

// Snapshot is the value returned by queries.
type Snapshot struct {
	Running   bool      `json:"running"`
	UpdatedAt time.Time `json:"updatedAt"`
	Stats     Stats     `json:"stats"`
	Links     []Link    `json:"links"`
}

// SortKey identifies a sortable column.
type SortKey string

const (
	SortHost         SortKey = "host"
	SortNode         SortKey = "node"
	SortUpload       SortKey = "upload"
	SortDownload     SortKey = "download"
	SortUploadRate   SortKey = "uploadRate"
	SortDownloadRate SortKey = "downloadRate"
	SortStartedAt    SortKey = "startedAt"
)

// Ordering describes one sort column and direction.
type Ordering struct {
	Key  SortKey
	Desc bool
}

// Query filters, sorts and pages the cached links.
type Query struct {
	Search string
	Active *bool
	Sort   []Ordering
	Offset int
	Limit  int
}

// Monitor polls the Clash API and maintains the link cache.
type Monitor struct {
	mu        sync.RWMutex
	client    *http.Client
	address   string
	secret    string
	resolve   Resolver
	running   bool
	cancel    context.CancelFunc
	done      chan struct{}
	links     map[string]*Link
	order     []string // oldest-first insertion order for eviction
	closedN   int
	stats     Stats
	lastSeen  map[string]sample
	selection map[string]string // selector/urltest group tag -> selected member tag
}

type sample struct {
	upload   int64
	download int64
	at       time.Time
}

// New creates a monitor. resolver may be nil, in which case the chain is
// rendered directly.
func New(resolver Resolver) *Monitor {
	return &Monitor{
		client:    &http.Client{Timeout: httpTimeout},
		resolve:   resolver,
		links:     make(map[string]*Link),
		lastSeen:  make(map[string]sample),
		selection: make(map[string]string),
	}
}

// Configure points the monitor at a Clash API controller. It does not start polling.
func (m *Monitor) Configure(address, secret string, resolver Resolver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.address, m.secret = address, secret
	if resolver != nil {
		m.resolve = resolver
	}
}

// Start begins polling. Calling Start on a running monitor is a no-op.
func (m *Monitor) Start(address, secret string, resolver Resolver) {
	m.Stop()
	m.mu.Lock()
	m.address, m.secret = address, secret
	if resolver != nil {
		m.resolve = resolver
	}
	m.running = true
	m.links = make(map[string]*Link)
	m.order = nil
	m.closedN = 0
	m.lastSeen = make(map[string]sample)
	m.selection = make(map[string]string)
	m.stats = Stats{TrackedCapacity: MaxLinks}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.done = make(chan struct{})
	done := m.done
	m.mu.Unlock()
	go m.run(ctx, done)
}

// Stop halts polling and waits for the loop to exit. The cache is retained so
// the UI can still show the last known links after a stop.
func (m *Monitor) Stop() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done, m.running = nil, nil, false
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// Reset clears the cache without changing the running state.
func (m *Monitor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.links = make(map[string]*Link)
	m.order = nil
	m.closedN = 0
	m.lastSeen = make(map[string]sample)
	m.selection = make(map[string]string)
	m.stats = Stats{TrackedCapacity: MaxLinks}
}

// Running reports whether the monitor is actively polling.
func (m *Monitor) Running() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

func (m *Monitor) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	if err := m.waitReady(ctx); err != nil {
		return
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	m.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.poll(ctx)
		}
	}
}

func (m *Monitor) waitReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := m.get(ctx, "/version", nil); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type connectionsPayload struct {
	UploadTotal   int64 `json:"uploadTotal"`
	DownloadTotal int64 `json:"downloadTotal"`
	Connections   []struct {
		ID       string   `json:"id"`
		Upload   int64    `json:"upload"`
		Download int64    `json:"download"`
		Start    string   `json:"start"`
		Chains   []string `json:"chains"`
		Metadata struct {
			Network         string `json:"network"`
			Type            string `json:"type"`
			Host            string `json:"host"`
			DestinationIP   string `json:"destinationIP"`
			DestinationPort string `json:"destinationPort"`
			SourceIP        string `json:"sourceIP"`
			SourcePort      string `json:"sourcePort"`
		} `json:"metadata"`
		Rule     string `json:"rule"`
		RuleType string `json:"rulePayload"`
	} `json:"connections"`
}

func (m *Monitor) poll(ctx context.Context) {
	var payload connectionsPayload
	if err := m.get(ctx, "/connections", &payload); err != nil {
		return
	}
	// Group selections (selector/urltest "now") let us resolve a connection's
	// chain to the concrete upstream member, since the chain only names the group.
	selection := m.fetchSelection(ctx)
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.selection = selection

	// Rates from the process-wide totals.
	var upRate, downRate int64
	if prev, ok := m.lastSeen["__total__"]; ok && now.After(prev.at) {
		elapsed := now.Sub(prev.at).Seconds()
		if elapsed > 0 {
			if payload.UploadTotal >= prev.upload {
				upRate = int64(float64(payload.UploadTotal-prev.upload) / elapsed)
			}
			if payload.DownloadTotal >= prev.download {
				downRate = int64(float64(payload.DownloadTotal-prev.download) / elapsed)
			}
		}
	}
	m.lastSeen["__total__"] = sample{upload: payload.UploadTotal, download: payload.DownloadTotal, at: now}

	seen := make(map[string]struct{}, len(payload.Connections))
	for _, c := range payload.Connections {
		seen[c.ID] = struct{}{}
		host := c.Metadata.Host
		if host == "" {
			host = c.Metadata.DestinationIP
		}
		if c.Metadata.DestinationPort != "" && host != "" {
			if !strings.Contains(host, ":") || strings.HasPrefix(host, "[") {
				host = net.JoinHostPort(strings.Trim(host, "[]"), c.Metadata.DestinationPort)
			}
		}
		startedAt, _ := time.Parse(time.RFC3339Nano, c.Start)
		node := m.nodeFor(c.Chains)

		link, exists := m.links[c.ID]
		if !exists {
			if len(m.order) >= MaxLinks {
				m.evictOldestLocked()
			}
			link = &Link{ID: c.ID, FirstSeenAt: now}
			m.links[c.ID] = link
			m.order = append(m.order, c.ID)
		}
		// Per-connection rate from its previous counters.
		if prev, ok := m.lastSeen[c.ID]; ok && now.After(prev.at) {
			elapsed := now.Sub(prev.at).Seconds()
			if elapsed > 0 {
				if c.Upload >= prev.upload {
					link.UploadRate = float64(c.Upload-prev.upload) / elapsed
				}
				if c.Download >= prev.download {
					link.DownloadRate = float64(c.Download-prev.download) / elapsed
				}
			}
		}
		m.lastSeen[c.ID] = sample{upload: c.Upload, download: c.Download, at: now}

		link.Host = host
		link.Network = c.Metadata.Network
		link.Type = c.Metadata.Type
		link.Upload = c.Upload
		link.Download = c.Download
		link.Node = node
		link.Chain = append([]string(nil), c.Chains...)
		if !startedAt.IsZero() {
			link.StartedAt = startedAt
		}
		link.LastSeenAt = now
		link.Active = true
	}

	// Mark vanished connections closed and decay their rates to zero.
	active := 0
	for id, link := range m.links {
		if _, ok := seen[id]; ok {
			active++
			continue
		}
		if link.Active {
			link.Active = false
			link.UploadRate, link.DownloadRate = 0, 0
			link.LastSeenAt = now
			m.closedN++
		}
		delete(m.lastSeen, id)
	}

	m.stats = Stats{
		Active:          active,
		Total:           len(m.links),
		UploadTotal:     payload.UploadTotal,
		DownloadTotal:   payload.DownloadTotal,
		UploadRate:      upRate,
		DownloadRate:    downRate,
		TrackedCapacity: MaxLinks,
	}
}

func (m *Monitor) evictOldestLocked() {
	// order is insertion-ordered; drop from the front, preferring closed links.
	for i, id := range m.order {
		link := m.links[id]
		if link == nil || !link.Active {
			delete(m.links, id)
			delete(m.lastSeen, id)
			m.order = append(m.order[:i], m.order[i+1:]...)
			return
		}
	}
	// All active: drop the oldest regardless.
	oldest := m.order[0]
	delete(m.links, oldest)
	delete(m.lastSeen, oldest)
	m.order = m.order[1:]
}

// fetchSelection reads the selector/urltest groups and returns a map of group
// tag to the member tag they currently route to (e.g. "auto" -> "pool-member-000").
func (m *Monitor) fetchSelection(ctx context.Context) map[string]string {
	var payload struct {
		Proxies map[string]struct {
			Type string `json:"type"`
			Now  string `json:"now"`
		} `json:"proxies"`
	}
	if err := m.get(ctx, "/proxies", &payload); err != nil {
		return nil
	}
	selection := make(map[string]string, len(payload.Proxies))
	for tag, proxy := range payload.Proxies {
		if (proxy.Type == "Selector" || proxy.Type == "URLTest") && proxy.Now != "" {
			selection[tag] = proxy.Now
		}
	}
	return selection
}

// nodeFor resolves a connection chain to a node name. Group tags are expanded
// through the live selection map so a chain like ["auto", "proxy"] resolves to
// the pool member that "auto" currently points at.
func (m *Monitor) nodeFor(chains []string) string {
	expanded := m.expandChains(chains)
	if m.resolve != nil {
		if name := m.resolve(expanded); name != "" {
			return name
		}
	}
	for _, chain := range expanded {
		if chain != "" && chain != "proxy" && chain != "auto" {
			return chain
		}
	}
	return "direct"
}

// expandChains follows selector/urltest selections so member tags surface.
// It guards against selection cycles with a small visit budget.
func (m *Monitor) expandChains(chains []string) []string {
	out := make([]string, 0, len(chains)+2)
	for _, chain := range chains {
		current := chain
		out = append(out, current)
		for depth := 0; depth < 4; depth++ {
			next, ok := m.selection[current]
			if !ok || next == "" || next == current {
				break
			}
			out = append(out, next)
			current = next
		}
	}
	return out
}

// Query returns a filtered, sorted, paged snapshot.
func (m *Monitor) Query(q Query) Snapshot {
	m.mu.RLock()
	links := make([]Link, 0, len(m.links))
	for _, link := range m.links {
		links = append(links, *link)
	}
	stats := m.stats
	running := m.running
	m.mu.RUnlock()

	links = filterLinks(links, q)
	sortLinks(links, q.Sort)

	total := len(links)
	if q.Offset > 0 {
		if q.Offset >= total {
			links = nil
		} else {
			links = links[q.Offset:]
		}
	}
	if q.Limit > 0 && len(links) > q.Limit {
		links = links[:q.Limit]
	}

	return Snapshot{
		Running:   running,
		UpdatedAt: time.Now().UTC(),
		Stats: Stats{
			Active:          stats.Active,
			Total:           total,
			UploadTotal:     stats.UploadTotal,
			DownloadTotal:   stats.DownloadTotal,
			UploadRate:      stats.UploadRate,
			DownloadRate:    stats.DownloadRate,
			TrackedCapacity: MaxLinks,
		},
		Links: links,
	}
}

func filterLinks(links []Link, q Query) []Link {
	if q.Search == "" && q.Active == nil {
		return links
	}
	search := strings.ToLower(strings.TrimSpace(q.Search))
	out := links[:0]
	for _, link := range links {
		if q.Active != nil && link.Active != *q.Active {
			continue
		}
		if search != "" {
			haystack := strings.ToLower(link.Host + " " + link.Node + " " + link.Network + " " + link.Type + " " + strings.Join(link.Chain, " "))
			if !strings.Contains(haystack, search) {
				continue
			}
		}
		out = append(out, link)
	}
	return out
}

func sortLinks(links []Link, ordering []Ordering) {
	if len(ordering) == 0 {
		// Default: active first, then most recent activity.
		ordering = []Ordering{{Key: SortStartedAt, Desc: true}}
	}
	sort.SliceStable(links, func(i, j int) bool {
		a, b := links[i], links[j]
		if a.Active != b.Active {
			return a.Active
		}
		for _, order := range ordering {
			cmp := compareLinks(a, b, order.Key)
			if cmp == 0 {
				continue
			}
			if order.Desc {
				return cmp > 0
			}
			return cmp < 0
		}
		return a.ID < b.ID
	})
}

func compareLinks(a, b Link, key SortKey) int {
	switch key {
	case SortHost:
		return strings.Compare(strings.ToLower(a.Host), strings.ToLower(b.Host))
	case SortNode:
		return strings.Compare(strings.ToLower(a.Node), strings.ToLower(b.Node))
	case SortUpload:
		return compareInt64(a.Upload, b.Upload)
	case SortDownload:
		return compareInt64(a.Download, b.Download)
	case SortUploadRate:
		return compareFloat(a.UploadRate, b.UploadRate)
	case SortDownloadRate:
		return compareFloat(a.DownloadRate, b.DownloadRate)
	case SortStartedAt:
		if a.StartedAt.Before(b.StartedAt) {
			return -1
		}
		if a.StartedAt.After(b.StartedAt) {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func compareInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func (m *Monitor) get(ctx context.Context, path string, output any) error {
	m.mu.RLock()
	address, secret := m.address, m.secret
	m.mu.RUnlock()
	if address == "" {
		return fmt.Errorf("controller address not configured")
	}
	endpoint := (&url.URL{Scheme: "http", Host: address, Path: path}).String()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	response, err := m.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("controller returned HTTP %d", response.StatusCode)
	}
	if output != nil {
		if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(output); err != nil {
			return err
		}
	}
	return nil
}
