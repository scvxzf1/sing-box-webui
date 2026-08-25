package dnsprofile

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"sing-box-webui/internal/events"
)

const (
	StrategyIPv4Only   = "ipv4_only"
	StrategyIPv6Only   = "ipv6_only"
	StrategyPreferIPv4 = "prefer_ipv4"
	StrategyPreferIPv6 = "prefer_ipv6"
)

var supportedServerTypes = map[string]struct{}{
	"udp": {}, "tcp": {}, "tls": {}, "https": {}, "quic": {}, "h3": {}, "local": {}, "hosts": {},
}

var supportedStrategies = map[string]struct{}{
	StrategyIPv4Only: {}, StrategyIPv6Only: {}, StrategyPreferIPv4: {}, StrategyPreferIPv6: {},
}

var tagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// FakeIP holds the fake-ip responder settings. The responder tag is not part
// of the persisted model: sing-box derives it as <final tag>-fakeip when the
// config is compiled.
type FakeIP struct {
	Enabled    bool   `json:"enabled"`
	Inet4Range string `json:"inet4Range,omitempty"`
	Inet6Range string `json:"inet6Range,omitempty"`
}

type Server struct {
	Tag    string `json:"tag"`
	Type   string `json:"type"`
	Server string `json:"server,omitempty"`
	Port   *int   `json:"port,omitempty"`
	Detour string `json:"detour,omitempty"`
}

type Profile struct {
	Servers  []Server `json:"servers"`
	Final    string   `json:"final"`
	Strategy string   `json:"strategy"`
	FakeIP   FakeIP   `json:"fakeIP"`
}

type UpdateInput = Profile

type Manager struct {
	mu      sync.RWMutex
	path    string
	profile Profile
	events  *events.Broker
	reload  func() error
}

// DefaultProfile returns the built-in DNS profile used before any profile was
// customized: a single plain-UDP resolver with IPv4 preference.
func DefaultProfile() Profile {
	return Profile{
		Servers:  []Server{{Tag: "dns-google", Type: "udp", Server: "8.8.8.8"}},
		Final:    "dns-google",
		Strategy: StrategyPreferIPv4,
		FakeIP:   FakeIP{Enabled: false},
	}
}

// FakeIPResponderTag returns the tag sing-box will use for the fakeip
// responder derived from this profile's final server.
func (p Profile) FakeIPResponderTag() string {
	return p.Final + "-fakeip"
}

func OpenManager(dataDirectory string, broker *events.Broker) (*Manager, error) {
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create DNS profile data directory: %w", err)
	}
	manager := &Manager{
		path:   filepath.Join(dataDirectory, "dns-profile.json"),
		events: broker,
	}
	if err := manager.load(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) SetReload(reload func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reload = reload
}

func (m *Manager) Reload() error {
	m.mu.RLock()
	reload := m.reload
	m.mu.RUnlock()
	if reload != nil {
		return reload()
	}
	return nil
}

func (m *Manager) Get() Profile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneProfile(m.profile)
}

func (m *Manager) Update(input UpdateInput) (Profile, error) {
	normalized, err := validateProfile(input)
	if err != nil {
		return Profile{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := m.profile
	m.profile = normalized
	if err := m.persistLocked(); err != nil {
		m.profile = previous
		return Profile{}, err
	}
	m.publish("dns-profile.updated", map[string]int{"serverCount": len(normalized.Servers)})
	return cloneProfile(m.profile), nil
}

func validateProfile(input Profile) (Profile, error) {
	if len(input.Servers) < 1 || len(input.Servers) > 8 {
		return Profile{}, fmt.Errorf("DNS profile must contain 1-8 servers")
	}
	servers := make([]Server, 0, len(input.Servers))
	seenTags := make(map[string]struct{})
	for _, server := range input.Servers {
		server.Tag = strings.TrimSpace(server.Tag)
		server.Type = strings.TrimSpace(server.Type)
		server.Server = strings.TrimSpace(server.Server)
		if !tagPattern.MatchString(server.Tag) {
			return Profile{}, fmt.Errorf("DNS server tag %q must be 1-32 characters of lowercase letters, digits, '-' or '_' and start with a letter or digit", server.Tag)
		}
		if _, duplicate := seenTags[server.Tag]; duplicate {
			return Profile{}, fmt.Errorf("DNS server tag %q is duplicated", server.Tag)
		}
		seenTags[server.Tag] = struct{}{}
		if _, ok := supportedServerTypes[server.Type]; !ok {
			return Profile{}, fmt.Errorf("unsupported DNS server type %q", server.Type)
		}
		server.Detour = strings.TrimSpace(server.Detour)
		if server.Detour != "" && server.Detour != "proxy" && server.Detour != "direct" && server.Detour != "block" {
			return Profile{}, fmt.Errorf("DNS server %q detour must be proxy, direct or block", server.Tag)
		}
		switch server.Type {
		case "local", "hosts":
			if server.Server != "" {
				return Profile{}, fmt.Errorf("DNS server %q of type %q must not set an address", server.Tag, server.Type)
			}
			server.Port = nil
			if server.Detour != "" {
				return Profile{}, fmt.Errorf("DNS server %q of type %q must not set detour", server.Tag, server.Type)
			}
		default:
			if err := validateServerAddress(server.Server); err != nil {
				return Profile{}, fmt.Errorf("DNS server %q: %w", server.Tag, err)
			}
			if server.Port != nil && (*server.Port < 1 || *server.Port > 65535) {
				return Profile{}, fmt.Errorf("DNS server %q port must be between 1 and 65535", server.Tag)
			}
		}
		servers = append(servers, server)
	}

	final := strings.TrimSpace(input.Final)
	if final == "" {
		if len(servers) == 1 {
			final = servers[0].Tag
		} else {
			return Profile{}, fmt.Errorf("final DNS server is required when multiple servers are configured")
		}
	}
	if _, ok := seenTags[final]; !ok {
		return Profile{}, fmt.Errorf("final DNS server %q must reference a configured server tag", final)
	}

	strategy := strings.TrimSpace(input.Strategy)
	if strategy == "" {
		strategy = StrategyPreferIPv4
	}
	if _, ok := supportedStrategies[strategy]; !ok {
		return Profile{}, fmt.Errorf("unsupported DNS strategy %q", strategy)
	}

	fakeIP := FakeIP{Enabled: input.FakeIP.Enabled}
	if fakeIP.Enabled {
		inet4Range := strings.TrimSpace(input.FakeIP.Inet4Range)
		if inet4Range == "" {
			inet4Range = "198.18.0.0/15"
		}
		inet6Range := strings.TrimSpace(input.FakeIP.Inet6Range)
		if inet6Range == "" {
			inet6Range = "fc00::/18"
		}
		if err := validateCIDR(inet4Range, false); err != nil {
			return Profile{}, fmt.Errorf("fakeip inet4 range: %w", err)
		}
		if err := validateCIDR(inet6Range, true); err != nil {
			return Profile{}, fmt.Errorf("fakeip inet6 range: %w", err)
		}
		fakeIP.Inet4Range = inet4Range
		fakeIP.Inet6Range = inet6Range
		if strategy != StrategyIPv4Only && strategy != StrategyPreferIPv4 {
			return Profile{}, fmt.Errorf("fakeip requires an IPv4-preferring strategy (ipv4_only or prefer_ipv4)")
		}
	}

	return Profile{Servers: servers, Final: final, Strategy: strategy, FakeIP: fakeIP}, nil
}

func validateServerAddress(value string) error {
	if value == "" {
		return fmt.Errorf("address is required")
	}
	if len(value) > 253 {
		return fmt.Errorf("address %q is too long", value)
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("address %q must not contain whitespace", value)
	}
	if _, err := strconv.Atoi(value); err == nil {
		return fmt.Errorf("address %q must be an IP address or a domain", value)
	}
	if ip, err := netip.ParseAddr(strings.TrimSuffix(value, ".")); err == nil {
		if ip.Zone() != "" {
			return fmt.Errorf("address %q must not include a zone", value)
		}
		return nil
	}
	labels := strings.Split(strings.TrimSuffix(value, "."), ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return fmt.Errorf("address %q is not a valid IP address or domain", value)
		}
		for index := 0; index < len(label); index++ {
			c := label[index]
			isAlphaNum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
			if !isAlphaNum && c != '-' && c != '_' {
				return fmt.Errorf("address %q contains invalid character %q", value, string(c))
			}
		}
	}
	return nil
}

func validateCIDR(value string, wantIPv6 bool) error {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return fmt.Errorf("%q is not a valid CIDR", value)
	}
	if prefix.Addr().Is6() != wantIPv6 {
		if wantIPv6 {
			return fmt.Errorf("%q is not an IPv6 range", value)
		}
		return fmt.Errorf("%q is not an IPv4 range", value)
	}
	return nil
}

func (m *Manager) load() error {
	content, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		m.profile = DefaultProfile()
		return nil
	}
	if err != nil {
		return fmt.Errorf("read DNS profile: %w", err)
	}
	var stored Profile
	if err := json.Unmarshal(content, &stored); err != nil {
		return fmt.Errorf("parse DNS profile: %w", err)
	}
	normalized, err := validateProfile(stored)
	if err != nil {
		return fmt.Errorf("parse DNS profile: %w", err)
	}
	m.profile = normalized
	return nil
}

func (m *Manager) persistLocked() error {
	content, err := json.MarshalIndent(m.profile, "", "  ")
	if err != nil {
		return fmt.Errorf("encode DNS profile: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(m.path), ".dns-profile-*.tmp")
	if err != nil {
		return fmt.Errorf("create DNS profile store: %w", err)
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
		return fmt.Errorf("commit DNS profile: %w", err)
	}
	committed = true
	return nil
}

func (m *Manager) publish(eventType string, payload any) {
	if m.events != nil {
		_, _ = m.events.Publish(eventType, payload)
	}
}

func cloneProfile(profile Profile) Profile {
	profile.Servers = append([]Server(nil), profile.Servers...)
	return profile
}
