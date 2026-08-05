// Package connectivity implements user-defined quick reachability checks. It
// stores a small list of named targets (default: GitHub, YouTube) and measures
// end-to-end HTTPS latency for one or all of them on demand.
//
// Probing is dual-path: every target is always measured with a direct
// connection, and additionally through the running proxy mixed inbound when
// one is available (system-proxy mode). In TUN mode the tunnel already
// intercepts the direct path system-wide, so the direct measurement reflects
// the proxied route.
package connectivity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxTargets    = 64
	probeTimeout  = 10 * time.Second
	maxNameLength = 64
)

// ProxyResolver reports the loopback address (host:port) of the currently
// running proxy mixed inbound, or an empty string when no local proxy port is
// available (stopped, or TUN mode where the tunnel handles routing).
type ProxyResolver interface {
	ProxyAddress() string
	ProxyRunning() bool
}

type Target struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type CreateInput struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type UpdateInput struct {
	Name *string `json:"name,omitempty"`
	URL  *string `json:"url,omitempty"`
}

type ResultStatus string

const (
	StatusOK      ResultStatus = "ok"
	StatusTimeout ResultStatus = "timeout"
	StatusFailed  ResultStatus = "failed"
)

// PathResult is the outcome of measuring a target over one network path.
type PathResult struct {
	Status    ResultStatus `json:"status"`
	LatencyMS *int64       `json:"latencyMs,omitempty"`
	Detail    string       `json:"detail,omitempty"`
}

// Result combines the direct and (optional) proxied measurements for a target.
type Result struct {
	TargetID string      `json:"targetId"`
	Name     string      `json:"name"`
	URL      string      `json:"url"`
	Direct   PathResult  `json:"direct"`
	Proxy    *PathResult `json:"proxy,omitempty"`
}

type TestResponse struct {
	Items []Result `json:"items"`
}

var (
	ErrNotFound        = errors.New("connectivity target not found")
	ErrProxyStopped    = errors.New("请先开启代理再进行节点检测")
	ErrInvalidProvider = errors.New("不支持的检测服务")
	errInvalidName     = errors.New("名称不能为空")
	errInvalidURL      = errors.New("URL 必须是有效的 http/https 地址")
)

type DiagnosticInput struct {
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
}

type DiagnosticResult struct {
	Kind         string `json:"kind"`
	Provider     string `json:"provider"`
	ProviderName string `json:"providerName"`
	URL          string `json:"url"`
	LatencyMS    int64  `json:"latencyMs"`
	IP           string `json:"ip,omitempty"`
	Country      string `json:"country,omitempty"`
	CountryCode  string `json:"countryCode,omitempty"`
	Region       string `json:"region,omitempty"`
	City         string `json:"city,omitempty"`
	ASN          string `json:"asn,omitempty"`
	Organization string `json:"organization,omitempty"`
	FraudScore   *int   `json:"fraudScore,omitempty"`
	Residential  *bool  `json:"residential,omitempty"`
}

type diagnosticProvider struct {
	Kind string
	Name string
	URL  string
}

var diagnosticProviders = map[string]diagnosticProvider{
	"123169":     {Kind: "quality", Name: "123169", URL: "https://my.123169.xyz/v1/info"},
	"ippure":     {Kind: "quality", Name: "IPPure", URL: "https://my.ippure.com/v1/info"},
	"ipify":      {Kind: "exit", Name: "ipify.org", URL: "https://api.ipify.org?format=json"},
	"ipsb":       {Kind: "exit", Name: "ip.sb", URL: "https://api.ip.sb/geoip"},
	"ifconfigme": {Kind: "exit", Name: "ifconfig.me", URL: "https://ifconfig.me/all.json"},
	"icanhazip":  {Kind: "exit", Name: "icanhazip.com", URL: "https://icanhazip.com"},
	"ipinfo":     {Kind: "exit", Name: "ipinfo.io", URL: "https://ipinfo.io/json"},
}

type Manager struct {
	mu       sync.Mutex
	path     string
	resolver ProxyResolver
	targets  []Target
}

// Open loads (or seeds) the persisted target list from dataDirectory.
func Open(dataDirectory string, resolver ProxyResolver) (*Manager, error) {
	if resolver == nil {
		return nil, fmt.Errorf("connectivity manager requires a proxy resolver")
	}
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		return nil, err
	}
	m := &Manager{
		path:     filepath.Join(dataDirectory, "connectivity-targets.json"),
		resolver: resolver,
	}
	content, err := os.ReadFile(m.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(content, &m.targets); err != nil {
			return nil, fmt.Errorf("decode connectivity targets: %w", err)
		}
	case errors.Is(err, os.ErrNotExist):
		m.targets = defaultTargets()
		if err := m.persistLocked(); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return m, nil
}

func defaultTargets() []Target {
	return []Target{
		{ID: newID(), Name: "GitHub", URL: "https://github.com"},
		{ID: newID(), Name: "YouTube", URL: "https://www.youtube.com/generate_204"},
	}
}

// List returns the stored targets in a stable (name) order.
func (m *Manager) List() []Target {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sortedLocked()
}

func (m *Manager) Create(input CreateInput) (Target, error) {
	name, targetURL, err := normalize(input.Name, input.URL)
	if err != nil {
		return Target{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.targets) >= maxTargets {
		return Target{}, fmt.Errorf("最多只能保存 %d 个测试目标", maxTargets)
	}
	target := Target{ID: newID(), Name: name, URL: targetURL}
	m.targets = append(m.targets, target)
	if err := m.persistLocked(); err != nil {
		return Target{}, err
	}
	return target, nil
}

func (m *Manager) Update(id string, input UpdateInput) (Target, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index, target := range m.targets {
		if target.ID != id {
			continue
		}
		name, targetURL := target.Name, target.URL
		if input.Name != nil {
			name = *input.Name
		}
		if input.URL != nil {
			targetURL = *input.URL
		}
		normalizedName, normalizedURL, err := normalize(name, targetURL)
		if err != nil {
			return Target{}, err
		}
		updated := Target{ID: target.ID, Name: normalizedName, URL: normalizedURL}
		m.targets[index] = updated
		if err := m.persistLocked(); err != nil {
			return Target{}, err
		}
		return updated, nil
	}
	return Target{}, ErrNotFound
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index, target := range m.targets {
		if target.ID == id {
			m.targets = append(m.targets[:index], m.targets[index+1:]...)
			return m.persistLocked()
		}
	}
	return ErrNotFound
}

// Test probes one target (id != "") or all targets (id == "") and returns the
// direct plus (when available) proxied latency for each.
func (m *Manager) Test(ctx context.Context, id string) (TestResponse, error) {
	proxyAddress := m.resolver.ProxyAddress()
	m.mu.Lock()
	targets := m.sortedLocked()
	m.mu.Unlock()

	if id != "" {
		found := false
		for _, target := range targets {
			if target.ID == id {
				targets = []Target{target}
				found = true
				break
			}
		}
		if !found {
			return TestResponse{}, ErrNotFound
		}
	}

	results := make([]Result, len(targets))
	var wait sync.WaitGroup
	for index, target := range targets {
		index, target := index, target
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[index] = measure(ctx, target, proxyAddress)
		}()
	}
	wait.Wait()
	return TestResponse{Items: results}, nil
}

func (m *Manager) Diagnose(ctx context.Context, input DiagnosticInput) (DiagnosticResult, error) {
	provider, ok := diagnosticProviders[input.Provider]
	if !ok || provider.Kind != input.Kind {
		return DiagnosticResult{}, ErrInvalidProvider
	}
	if !m.resolver.ProxyRunning() {
		return DiagnosticResult{}, ErrProxyStopped
	}
	return diagnose(ctx, input.Provider, provider, m.resolver.ProxyAddress())
}

func diagnose(ctx context.Context, providerID string, provider diagnosticProvider, proxyAddress string) (DiagnosticResult, error) {
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: probeTimeout}).DialContext,
		TLSHandshakeTimeout: probeTimeout,
	}
	if proxyAddress != "" {
		proxyURL, err := url.Parse("http://" + proxyAddress)
		if err != nil {
			return DiagnosticResult{}, fmt.Errorf("代理地址无效")
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	defer transport.CloseIdleConnections()

	requestCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, provider.URL, nil)
	if err != nil {
		return DiagnosticResult{}, fmt.Errorf("检测地址无效")
	}
	request.Header.Set("Accept", "application/json, text/plain;q=0.9")
	request.Header.Set("User-Agent", "sing-box-webui-diagnostic")
	startedAt := time.Now()
	response, err := (&http.Client{Transport: transport, Timeout: probeTimeout}).Do(request)
	if err != nil {
		if requestCtx.Err() != nil {
			return DiagnosticResult{}, fmt.Errorf("检测超时")
		}
		return DiagnosticResult{}, fmt.Errorf("检测失败：%s", summarizeError(err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return DiagnosticResult{}, fmt.Errorf("检测服务返回 HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		return DiagnosticResult{}, fmt.Errorf("读取检测结果失败")
	}
	if len(body) > 64<<10 {
		return DiagnosticResult{}, fmt.Errorf("检测结果过大")
	}
	latency := time.Since(startedAt).Milliseconds()
	if latency < 1 {
		latency = 1
	}
	result := DiagnosticResult{
		Kind: provider.Kind, Provider: providerID, ProviderName: provider.Name,
		URL: provider.URL, LatencyMS: latency,
	}
	parseDiagnosticBody(body, &result)
	if result.IP == "" {
		return DiagnosticResult{}, fmt.Errorf("检测服务未返回 IP 地址")
	}
	return result, nil
}

func parseDiagnosticBody(body []byte, result *DiagnosticResult) {
	var values map[string]any
	if err := json.Unmarshal(body, &values); err != nil {
		result.IP = strings.TrimSpace(string(body))
		return
	}
	result.IP = firstString(values, "ip", "ip_addr", "query")
	result.Country = firstString(values, "country", "country_name")
	result.CountryCode = firstString(values, "countryCode", "country_code")
	result.Region = firstString(values, "region", "regionName", "region_name")
	result.City = firstString(values, "city")
	result.Organization = firstString(values, "asOrganization", "organization", "org", "isp", "asn_organization")
	result.ASN = scalarString(values["asn"])
	if value, ok := numberInt(values["fraudScore"]); ok {
		result.FraudScore = &value
	}
	if value, ok := values["isResidential"].(bool); ok {
		result.Residential = &value
	}
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := scalarString(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func scalarString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return fmt.Sprintf("%.0f", typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func numberInt(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err == nil
	default:
		return 0, false
	}
}

// measure runs the direct probe and, when proxyAddress is non-empty, the
// proxied probe concurrently, then combines them into a single Result.
func measure(ctx context.Context, target Target, proxyAddress string) Result {
	result := Result{TargetID: target.ID, Name: target.Name, URL: target.URL}
	if proxyAddress == "" {
		result.Direct = probe(ctx, target.URL, "")
		return result
	}
	var direct, proxied PathResult
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		direct = probe(ctx, target.URL, "")
	}()
	go func() {
		defer wait.Done()
		proxied = probe(ctx, target.URL, proxyAddress)
	}()
	wait.Wait()
	result.Direct = direct
	result.Proxy = &proxied
	return result
}

// probe issues a single GET against targetURL. When proxyAddress is non-empty
// the request is routed through that HTTP proxy; otherwise it goes direct.
func probe(ctx context.Context, targetURL, proxyAddress string) PathResult {
	result := PathResult{Status: StatusFailed}
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: probeTimeout}).DialContext,
		TLSHandshakeTimeout: probeTimeout,
	}
	if proxyAddress != "" {
		proxyURL, err := url.Parse("http://" + proxyAddress)
		if err != nil {
			result.Detail = "代理地址无效"
			return result
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: probeTimeout}

	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		result.Detail = "目标地址无效"
		return result
	}
	request.Header.Set("User-Agent", "sing-box-webui-connectivity")

	startedAt := time.Now()
	response, err := client.Do(request)
	if err != nil {
		if probeCtx.Err() != nil {
			result.Status = StatusTimeout
			result.Detail = "连接超时"
			return result
		}
		result.Detail = "连接失败：" + summarizeError(err)
		return result
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))

	delay := time.Since(startedAt).Milliseconds()
	if delay < 1 {
		delay = 1
	}
	result.Status = StatusOK
	result.LatencyMS = &delay
	result.Detail = fmt.Sprintf("HTTP %d", response.StatusCode)
	return result
}

// summarizeError trims noisy Go error text down to the actionable cause.
func summarizeError(err error) string {
	text := err.Error()
	if index := strings.LastIndex(text, ": "); index >= 0 {
		text = text[index+2:]
	}
	return text
}

func normalize(name, rawURL string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", errInvalidName
	}
	if len([]rune(name)) > maxNameLength {
		return "", "", fmt.Errorf("名称不能超过 %d 个字符", maxNameLength)
	}
	rawURL = strings.TrimSpace(rawURL)
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", "", errInvalidURL
	}
	return name, rawURL, nil
}

func (m *Manager) sortedLocked() []Target {
	result := append([]Target(nil), m.targets...)
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].ID < result[j].ID
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func (m *Manager) persistLocked() error {
	content, err := json.MarshalIndent(m.targets, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(m.path), ".connectivity-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, m.path)
}

func newID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("t-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}
