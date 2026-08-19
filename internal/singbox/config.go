package singbox

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sing-box-webui/internal/dnsprofile"
	"sing-box-webui/internal/subscription"
)

type URLTestOptions struct {
	URL                          string
	Interval                     time.Duration
	Tolerance                    int
	IdleTimeout                  time.Duration
	InterruptExistingConnections bool
	ControllerAddress            string
	ControllerSecret             string
}

type ControllerOptions struct {
	Address string
	Secret  string
}

type ProxyMode string

const (
	ModeSystemProxy ProxyMode = "system-proxy"
	ModeTUN         ProxyMode = "tun"
)

// DefaultTUNAddress is the IPv4 CIDR assigned to the system TUN inbound. It is
// configurable at startup so deployments can avoid local or container routes.
var DefaultTUNAddress = "198.20.0.1/30"

func BuildConfig(node subscription.Node, mode ProxyMode, mixedPort uint16) ([]byte, error) {
	return BuildConfigWithRules(node, mode, mixedPort, nil)
}

func BuildConfigWithRules(node subscription.Node, mode ProxyMode, mixedPort uint16, routeRules []map[string]any) ([]byte, error) {
	return BuildConfigWithController(node, mode, mixedPort, routeRules, ControllerOptions{}, dnsprofile.DefaultProfile(), false)
}

// BuildConfigWithController builds a single-node config, optionally exposing the
// Clash API so the control plane can inspect live connections and traffic.
func BuildConfigWithController(node subscription.Node, mode ProxyMode, mixedPort uint16, routeRules []map[string]any, controller ControllerOptions, dns dnsprofile.Profile, allowLan bool) ([]byte, error) {
	if mode != ModeSystemProxy && mode != ModeTUN {
		return nil, fmt.Errorf("unsupported proxy mode %q", mode)
	}
	if (controller.Address == "") != (controller.Secret == "") {
		return nil, fmt.Errorf("controller address and secret must be configured together")
	}
	if err := validateTUNProfile(mode, dns); err != nil {
		return nil, err
	}
	outbound, err := buildOutbound(node, "proxy")
	if err != nil {
		return nil, err
	}

	inbounds := []any{buildInbound(mode, mixedPort, allowLan)}
	if extra := lanInbound(mode, mixedPort, allowLan); extra != nil {
		inbounds = append(inbounds, extra)
	}

	config := map[string]any{
		"log": map[string]any{
			"level":     "info",
			"timestamp": true,
		},
		"inbounds": inbounds,
		"outbounds": []any{
			outbound,
			map[string]any{"type": "direct", "tag": "direct"},
			map[string]any{"type": "block", "tag": "block"},
		},
		"route": buildRoute(mode, routeRules, dns),
	}
	if mode == ModeTUN {
		config["dns"] = buildTUNDNS(dns)
	}
	if controller.Address != "" {
		config["experimental"] = clashAPIConfig(controller.Address, controller.Secret)
	}
	return json.MarshalIndent(config, "", "  ")
}

func clashAPIConfig(address, secret string) map[string]any {
	return map[string]any{"clash_api": map[string]any{
		"external_controller":         address,
		"secret":                      secret,
		"access_control_allow_origin": []string{"http://127.0.0.1"},
	}}
}

func BuildPoolConfig(nodes []subscription.Node, mode ProxyMode, mixedPort uint16, options URLTestOptions) ([]byte, error) {
	return BuildPoolConfigWithRules(nodes, mode, mixedPort, options, nil)
}

func BuildPoolConfigWithRules(nodes []subscription.Node, mode ProxyMode, mixedPort uint16, options URLTestOptions, routeRules []map[string]any) ([]byte, error) {
	return BuildPoolConfigWithDNS(nodes, mode, mixedPort, options, routeRules, dnsprofile.DefaultProfile(), false)
}

// BuildPoolConfigWithDNS compiles a pool config with an explicit DNS profile.
func BuildPoolConfigWithDNS(nodes []subscription.Node, mode ProxyMode, mixedPort uint16, options URLTestOptions, routeRules []map[string]any, dns dnsprofile.Profile, allowLan bool) ([]byte, error) {
	if len(nodes) < 2 {
		return nil, fmt.Errorf("node pool requires at least 2 available members")
	}
	if mode != ModeSystemProxy && mode != ModeTUN {
		return nil, fmt.Errorf("unsupported proxy mode %q", mode)
	}
	if options.Interval < 15*time.Second || options.Interval > time.Hour {
		return nil, fmt.Errorf("urltest interval must be between 15s and 1h")
	}
	if options.Tolerance < 0 || options.Tolerance > 1000 {
		return nil, fmt.Errorf("urltest tolerance must be between 0 and 1000ms")
	}
	if options.URL == "" {
		options.URL = "https://cp.cloudflare.com/generate_204"
	}
	parsedURL, err := url.ParseRequestURI(options.URL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" {
		return nil, fmt.Errorf("urltest URL must be a valid HTTPS URL")
	}
	if options.IdleTimeout == 0 {
		options.IdleTimeout = 30 * time.Minute
	}
	if options.IdleTimeout < time.Minute || options.IdleTimeout > 24*time.Hour {
		return nil, fmt.Errorf("urltest idle timeout must be between 1m and 24h")
	}
	if options.Interval > options.IdleTimeout {
		return nil, fmt.Errorf("urltest interval cannot exceed idle timeout")
	}
	if (options.ControllerAddress == "") != (options.ControllerSecret == "") {
		return nil, fmt.Errorf("health controller address and secret must be configured together")
	}
	if err := validateTUNProfile(mode, dns); err != nil {
		return nil, err
	}

	outbounds := make([]any, 0, len(nodes)+4)
	tags := make([]string, 0, len(nodes))
	for index, node := range nodes {
		tag := PoolMemberTag(index)
		outbound, err := buildOutbound(node, tag)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", node.Name, err)
		}
		outbounds = append(outbounds, outbound)
		tags = append(tags, tag)
	}
	outbounds = append(outbounds,
		map[string]any{
			"type": "urltest", "tag": "auto", "outbounds": tags,
			"url": options.URL, "interval": options.Interval.String(),
			"tolerance": options.Tolerance, "idle_timeout": options.IdleTimeout.String(),
			"interrupt_exist_connections": options.InterruptExistingConnections,
		},
		map[string]any{
			"type": "selector", "tag": "proxy", "outbounds": append(append([]string{"auto"}, tags...), "block"),
			"default": "auto", "interrupt_exist_connections": options.InterruptExistingConnections,
		},
		map[string]any{"type": "direct", "tag": "direct"},
		map[string]any{"type": "block", "tag": "block"},
	)

	inbounds := []any{buildInbound(mode, mixedPort, allowLan)}
	if extra := lanInbound(mode, mixedPort, allowLan); extra != nil {
		inbounds = append(inbounds, extra)
	}
	config := map[string]any{
		"log":       map[string]any{"level": "info", "timestamp": true},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route":     buildRoute(mode, routeRules, dns),
	}
	if mode == ModeTUN {
		config["dns"] = buildTUNDNS(dns)
	}
	if options.ControllerAddress != "" {
		config["experimental"] = clashAPIConfig(options.ControllerAddress, options.ControllerSecret)
	}
	return json.MarshalIndent(config, "", "  ")
}

func PoolMemberTag(index int) string {
	return fmt.Sprintf("pool-member-%03d", index)
}

func buildInbound(mode ProxyMode, mixedPort uint16, allowLan bool) map[string]any {
	if mode == ModeSystemProxy {
		listen := "127.0.0.1"
		if allowLan {
			listen = "0.0.0.0"
		}
		return map[string]any{"type": "mixed", "tag": "mixed-in", "listen": listen, "listen_port": mixedPort}
	}
	return map[string]any{
		"type": "tun", "tag": "tun-in", "interface_name": "singtun0",
		"address": []string{DefaultTUNAddress, "fdfe:dcba:9876::1/126"}, "auto_route": true, "strict_route": true, "stack": "system",
	}
}

func validateTUNProfile(mode ProxyMode, profile dnsprofile.Profile) error {
	return validateTUNProfileAddress(mode, profile, DefaultTUNAddress)
}

func validateTUNProfileAddress(mode ProxyMode, profile dnsprofile.Profile, address string) error {
	if mode != ModeTUN {
		return nil
	}
	tunPrefix, err := netip.ParsePrefix(address)
	if err != nil || tunPrefix.Bits() != 30 || !tunPrefix.Addr().Is4() {
		return fmt.Errorf("invalid TUN address %q: expected an IPv4 /30 CIDR", address)
	}
	if !profile.FakeIP.Enabled || profile.FakeIP.Inet4Range == "" {
		return nil
	}
	fakePrefix, err := netip.ParsePrefix(profile.FakeIP.Inet4Range)
	if err != nil || !fakePrefix.Addr().Is4() {
		return fmt.Errorf("invalid Fake IP IPv4 range %q", profile.FakeIP.Inet4Range)
	}
	tunPrefix = tunPrefix.Masked()
	fakePrefix = fakePrefix.Masked()
	if tunPrefix.Contains(fakePrefix.Addr()) || fakePrefix.Contains(tunPrefix.Addr()) {
		return fmt.Errorf("TUN address %q overlaps Fake IP range %q", address, profile.FakeIP.Inet4Range)
	}
	return nil
}

// lanInbound returns an extra mixed inbound bound to 0.0.0.0 so LAN devices can
// point their proxy at this host. It is only needed in TUN mode, where the
// primary inbound is the tun interface; in system-proxy mode the primary mixed
// inbound is already rebound to 0.0.0.0 by buildInbound.
func lanInbound(mode ProxyMode, mixedPort uint16, allowLan bool) map[string]any {
	if !allowLan || mode != ModeTUN {
		return nil
	}
	return map[string]any{"type": "mixed", "tag": "mixed-lan", "listen": "0.0.0.0", "listen_port": mixedPort}
}

// buildTUNDNS compiles the persisted DNS profile into a sing-box DNS section.
// The profile is expected to have passed dnsprofile validation; the compiler
// additionally guarantees that plain servers (udp/tcp/tls/https/quic/h3) can
// bootstrap themselves by rewriting domain addresses onto a synthesized
// bootstrap resolver, so a saved profile can never produce a config that
// sing-box refuses to start.
func buildTUNDNS(profile dnsprofile.Profile) map[string]any {
	servers := make([]any, 0, len(profile.Servers)+2)
	bootstrapTag := ""
	for _, server := range profile.Servers {
		compiled := map[string]any{"type": server.Type, "tag": server.Tag}
		switch server.Type {
		case "local", "hosts":
			// no address fields
		default:
			if !isIPLiteral(server.Server) {
				if bootstrapTag == "" {
					bootstrapTag = uniqueDNSTag(profile, "dns-bootstrap")
					servers = append(servers, map[string]any{
						"type": "udp", "tag": bootstrapTag, "server": "223.5.5.5",
					})
				}
				compiled["domain_resolver"] = map[string]any{"server": bootstrapTag}
			}
			compiled["server"] = server.Server
			if server.Port != nil {
				compiled["server_port"] = *server.Port
			}
		}
		servers = append(servers, compiled)
	}

	final := profile.Final
	if profile.FakeIP.Enabled {
		final = profile.FakeIPResponderTag()
		servers = append(servers, map[string]any{
			"type":        "fakeip",
			"tag":         profile.FakeIPResponderTag(),
			"inet4_range": profile.FakeIP.Inet4Range,
			"inet6_range": profile.FakeIP.Inet6Range,
		})
	}
	result := map[string]any{
		"servers":  servers,
		"final":    final,
		"strategy": profile.Strategy,
		// Record the IP→domain mapping of every hijacked answer so IP-only
		// TUN connections can still be labelled with their domain in the
		// Clash API (and the links view).
		"reverse_mapping": true,
	}
	if profile.FakeIP.Enabled {
		result["rules"] = []any{
			map[string]any{"query_type": []string{"A", "AAAA"}, "server": profile.FakeIPResponderTag()},
		}
	}
	return result
}

func buildRoute(mode ProxyMode, routeRules []map[string]any, dns dnsprofile.Profile) map[string]any {
	rules := append([]map[string]any(nil), routeRules...)
	if mode == ModeTUN {
		// hijack-dns must precede user rules; sniff runs last so port-53
		// traffic is answered, not sniffed. Sniffing recovers the domain
		// (TLS SNI / HTTP Host / QUIC) that TUN connections otherwise lose
		// after the hijacked DNS exchange, populating Clash API host fields.
		rules = append([]map[string]any{{"port": 53, "action": "hijack-dns"}}, rules...)
		rules = append(rules, map[string]any{"action": "sniff"})
	}
	route := map[string]any{
		"auto_detect_interface": true,
		"final":                 "proxy",
		"rules":                 rules,
	}
	if mode == ModeTUN {
		// Outbound dials resolve their server domains through this resolver;
		// without it sing-box 1.13 rejects TUN configs that define DNS servers.
		route["default_domain_resolver"] = map[string]any{"server": dns.Final}
	}
	return route
}

// uniqueDNSTag picks a tag that does not collide with any user-configured
// server tag. The fakeip tag is synthesized from a server tag itself, so it is
// collision-free by construction.
func uniqueDNSTag(profile dnsprofile.Profile, base string) string {
	used := map[string]struct{}{}
	for _, server := range profile.Servers {
		used[server.Tag] = struct{}{}
	}
	if _, taken := used[base]; !taken {
		return base
	}
	for index := 2; ; index++ {
		candidate := fmt.Sprintf("%s-%d", base, index)
		if _, taken := used[candidate]; !taken {
			return candidate
		}
	}
}

func isIPLiteral(value string) bool {
	return net.ParseIP(strings.TrimSuffix(value, ".")) != nil
}

func buildOutbound(node subscription.Node, tag string) (map[string]any, error) {
	if err := subscription.ValidateNode(node); err != nil {
		return nil, err
	}
	outbound := map[string]any{
		"type":        node.Type,
		"tag":         tag,
		"server":      node.Server,
		"server_port": node.Port,
	}
	switch node.Type {
	case "shadowsocks":
		outbound["method"] = node.Method
		outbound["password"] = node.Password
	case "socks":
		outbound["version"] = "5"
		setIfNotEmpty(outbound, "username", node.Username)
		setIfNotEmpty(outbound, "password", node.Password)
	case "http":
		setIfNotEmpty(outbound, "username", node.Username)
		setIfNotEmpty(outbound, "password", node.Password)
	case "trojan":
		outbound["password"] = node.Password
	case "vless":
		outbound["uuid"] = node.UUID
		setIfNotEmpty(outbound, "flow", node.Flow)
	case "vmess":
		outbound["uuid"] = node.UUID
		outbound["security"] = firstNonEmpty(node.Security, "auto")
		outbound["alter_id"] = node.AlterID
	case "hysteria2":
		outbound["password"] = node.Password
		if node.Obfs != "" {
			outbound["obfs"] = map[string]any{"type": node.Obfs, "password": node.ObfsPassword}
		}
	case "tuic":
		outbound["uuid"] = node.UUID
		outbound["password"] = node.Password
		setIfNotEmpty(outbound, "congestion_control", node.CongestionControl)
		setIfNotEmpty(outbound, "udp_relay_mode", node.UDPRelayMode)
	default:
		return nil, fmt.Errorf("unsupported outbound type %q", node.Type)
	}

	if node.TLS.Enabled {
		tlsConfig := map[string]any{"enabled": true}
		setIfNotEmpty(tlsConfig, "server_name", node.TLS.ServerName)
		if node.TLS.Insecure {
			tlsConfig["insecure"] = true
		}
		if len(node.TLS.ALPN) > 0 {
			tlsConfig["alpn"] = node.TLS.ALPN
		}
		if node.TLS.UTLSFingerprint != "" {
			tlsConfig["utls"] = map[string]any{
				"enabled":     true,
				"fingerprint": node.TLS.UTLSFingerprint,
			}
		}
		if node.TLS.Reality.Enabled {
			reality := map[string]any{
				"enabled":    true,
				"public_key": node.TLS.Reality.PublicKey,
			}
			setIfNotEmpty(reality, "short_id", node.TLS.Reality.ShortID)
			tlsConfig["reality"] = reality
		}
		outbound["tls"] = tlsConfig
	}
	if node.Transport.Type != "" {
		transport := map[string]any{"type": node.Transport.Type}
		setIfNotEmpty(transport, "path", node.Transport.Path)
		setIfNotEmpty(transport, "service_name", node.Transport.ServiceName)
		if len(node.Transport.Headers) > 0 {
			transport["headers"] = node.Transport.Headers
		}
		outbound["transport"] = transport
	}
	return outbound, nil
}

func BuildLatencyConfig(nodes []subscription.Node, listenAddresses []string) ([]byte, []string, error) {
	if len(nodes) != len(listenAddresses) {
		return nil, nil, fmt.Errorf("latency nodes and listen addresses must have equal length")
	}
	outbounds := make([]any, 0, len(nodes))
	inbounds := make([]any, 0, len(nodes))
	rules := make([]any, 0, len(nodes))
	tags := make([]string, 0, len(nodes))
	for index, node := range nodes {
		tag := fmt.Sprintf("probe-%03d", index)
		outbound, err := buildOutbound(node, tag)
		if err != nil {
			return nil, nil, err
		}
		outbounds = append(outbounds, outbound)
		tags = append(tags, tag)
		host, portText, err := net.SplitHostPort(listenAddresses[index])
		if err != nil {
			return nil, nil, fmt.Errorf("parse latency listen address: %w", err)
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			return nil, nil, fmt.Errorf("parse latency listen port: %w", err)
		}
		inboundTag := fmt.Sprintf("probe-in-%03d", index)
		inbounds = append(inbounds, map[string]any{
			"type": "mixed", "tag": inboundTag, "listen": host, "listen_port": port,
		})
		rules = append(rules, map[string]any{
			"inbound": []string{inboundTag}, "action": "route", "outbound": tag,
		})
	}
	config := map[string]any{
		"log":       map[string]any{"level": "error", "timestamp": true},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route":     map[string]any{"rules": rules},
	}
	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("encode latency config: %w", err)
	}
	return content, tags, nil
}

func setIfNotEmpty(target map[string]any, key, value string) {
	if value != "" {
		target[key] = value
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
