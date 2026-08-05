package singbox

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

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

func BuildConfig(node subscription.Node, mode ProxyMode, mixedPort uint16) ([]byte, error) {
	return BuildConfigWithRules(node, mode, mixedPort, nil)
}

func BuildConfigWithRules(node subscription.Node, mode ProxyMode, mixedPort uint16, routeRules []map[string]any) ([]byte, error) {
	return BuildConfigWithController(node, mode, mixedPort, routeRules, ControllerOptions{})
}

// BuildConfigWithController builds a single-node config, optionally exposing the
// Clash API so the control plane can inspect live connections and traffic.
func BuildConfigWithController(node subscription.Node, mode ProxyMode, mixedPort uint16, routeRules []map[string]any, controller ControllerOptions) ([]byte, error) {
	if mode != ModeSystemProxy && mode != ModeTUN {
		return nil, fmt.Errorf("unsupported proxy mode %q", mode)
	}
	if (controller.Address == "") != (controller.Secret == "") {
		return nil, fmt.Errorf("controller address and secret must be configured together")
	}
	outbound, err := buildOutbound(node, "proxy")
	if err != nil {
		return nil, err
	}

	var inbound map[string]any
	if mode == ModeSystemProxy {
		inbound = map[string]any{
			"type":        "mixed",
			"tag":         "mixed-in",
			"listen":      "127.0.0.1",
			"listen_port": mixedPort,
		}
	} else {
		inbound = map[string]any{
			"type":           "tun",
			"tag":            "tun-in",
			"interface_name": "singtun0",
			"address":        []string{"172.19.0.1/30"},
			"auto_route":     true,
			"strict_route":   true,
			"stack":          "system",
		}
	}

	config := map[string]any{
		"log": map[string]any{
			"level":     "info",
			"timestamp": true,
		},
		"inbounds": []any{inbound},
		"outbounds": []any{
			outbound,
			map[string]any{"type": "direct", "tag": "direct"},
			map[string]any{"type": "block", "tag": "block"},
		},
		"route": map[string]any{
			"auto_detect_interface": true,
			"final":                 "proxy",
			"rules":                 routeRules,
		},
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

	outbounds := make([]any, 0, len(nodes)+4)
	tags := make([]string, 0, len(nodes))
	for index, node := range nodes {
		tag := PoolMemberTag(index)
		outbound, err := buildOutbound(node, tag)
		if err != nil {
			return nil, err
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

	config := map[string]any{
		"log":       map[string]any{"level": "info", "timestamp": true},
		"inbounds":  []any{buildInbound(mode, mixedPort)},
		"outbounds": outbounds,
		"route":     map[string]any{"auto_detect_interface": true, "final": "proxy", "rules": routeRules},
	}
	if options.ControllerAddress != "" {
		config["experimental"] = clashAPIConfig(options.ControllerAddress, options.ControllerSecret)
	}
	return json.MarshalIndent(config, "", "  ")
}

func PoolMemberTag(index int) string {
	return fmt.Sprintf("pool-member-%03d", index)
}

func buildInbound(mode ProxyMode, mixedPort uint16) map[string]any {
	if mode == ModeSystemProxy {
		return map[string]any{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": mixedPort}
	}
	return map[string]any{
		"type": "tun", "tag": "tun-in", "interface_name": "singtun0",
		"address": []string{"172.19.0.1/30"}, "auto_route": true, "strict_route": true, "stack": "system",
	}
}

func buildOutbound(node subscription.Node, tag string) (map[string]any, error) {
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
