package singbox

import (
	"encoding/json"
	"testing"
	"time"

	"sing-box-webui/internal/subscription"
)

func TestBuildConfig(t *testing.T) {
	t.Parallel()
	node := subscription.Node{
		Name:      "test",
		Type:      "vless",
		Server:    "proxy.example.com",
		Port:      443,
		UUID:      "11111111-1111-1111-1111-111111111111",
		TLS:       subscription.TLS{Enabled: true, ServerName: "proxy.example.com"},
		Transport: subscription.Transport{Type: "ws", Path: "/ws", Headers: map[string]string{"Host": "cdn.example.com"}},
	}

	for _, mode := range []ProxyMode{ModeSystemProxy, ModeTUN} {
		content, err := BuildConfig(node, mode, 2080)
		if err != nil {
			t.Fatalf("BuildConfig(%s) error = %v", mode, err)
		}
		var config map[string]any
		if err := json.Unmarshal(content, &config); err != nil {
			t.Fatalf("generated config is invalid JSON: %v", err)
		}
		if len(config["inbounds"].([]any)) != 1 || len(config["outbounds"].([]any)) != 3 {
			t.Fatalf("unexpected config shape: %+v", config)
		}
	}
}

func TestBuildConfigIncludesEnabledRouteRulesBeforeGlobalFinal(t *testing.T) {
	t.Parallel()
	node := subscription.Node{Type: "shadowsocks", Server: "1.1.1.1", Port: 443, Method: "aes-128-gcm", Password: "secret"}
	rules := []map[string]any{{"domain_suffix": []string{"example.com"}, "action": "route", "outbound": "direct"}}
	content, err := BuildConfigWithRules(node, ModeSystemProxy, 2080, rules)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	route := config["route"].(map[string]any)
	if route["final"] != "proxy" || len(route["rules"].([]any)) != 1 {
		t.Fatalf("route = %#v", route)
	}
}

func TestBuildPoolConfigUsesURLTestOutbound(t *testing.T) {
	t.Parallel()
	nodes := []subscription.Node{
		{Type: "shadowsocks", Server: "1.1.1.1", Port: 443, Method: "aes-128-gcm", Password: "one"},
		{Type: "trojan", Server: "8.8.8.8", Port: 443, Password: "two", TLS: subscription.TLS{Enabled: true, ServerName: "example.com"}},
	}
	content, err := BuildPoolConfig(nodes, ModeSystemProxy, 2080, URLTestOptions{
		URL: "https://www.gstatic.com/generate_204", Interval: time.Minute, Tolerance: 80,
		IdleTimeout: 10 * time.Minute, InterruptExistingConnections: true,
		ControllerAddress: "127.0.0.1:39092", ControllerSecret: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	outbounds := config["outbounds"].([]any)
	if len(outbounds) != 6 {
		t.Fatalf("outbounds = %d, want 6", len(outbounds))
	}
	urltest := outbounds[2].(map[string]any)
	if urltest["type"] != "urltest" || urltest["tag"] != "auto" || urltest["url"] != "https://www.gstatic.com/generate_204" || urltest["interval"] != "1m0s" || urltest["idle_timeout"] != "10m0s" || urltest["tolerance"] != float64(80) {
		t.Fatalf("unexpected urltest outbound: %+v", urltest)
	}
	if interrupt, ok := urltest["interrupt_exist_connections"].(bool); !ok || !interrupt {
		t.Fatalf("interrupt_exist_connections = %#v, want true", urltest["interrupt_exist_connections"])
	}
	selector := outbounds[3].(map[string]any)
	if selector["type"] != "selector" || selector["tag"] != "proxy" || selector["default"] != "auto" {
		t.Fatalf("unexpected selector outbound: %+v", selector)
	}
	experimental := config["experimental"].(map[string]any)
	clashAPI := experimental["clash_api"].(map[string]any)
	if clashAPI["external_controller"] != "127.0.0.1:39092" || clashAPI["secret"] != "test-secret" {
		t.Fatalf("unexpected clash API: %+v", clashAPI)
	}
}

func TestBuildLatencyConfig(t *testing.T) {
	t.Parallel()
	nodes := []subscription.Node{
		{Type: "shadowsocks", Server: "1.1.1.1", Port: 443, Method: "aes-128-gcm", Password: "secret"},
		{Type: "trojan", Server: "8.8.8.8", Port: 443, Password: "secret", TLS: subscription.TLS{Enabled: true, ServerName: "example.com"}},
	}
	content, tags, err := BuildLatencyConfig(nodes, []string{"127.0.0.1:39090", "127.0.0.1:39091"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 || tags[0] == tags[1] {
		t.Fatalf("unexpected tags: %v", tags)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	if len(config["inbounds"].([]any)) != 2 {
		t.Fatalf("unexpected latency inbounds: %+v", config["inbounds"])
	}
}

func TestBuildConfigIncludesRealityAndUTLS(t *testing.T) {
	t.Parallel()
	node := subscription.Node{
		Type: "vless", Server: "proxy.example.com", Port: 443,
		UUID: "11111111-1111-1111-1111-111111111111", Flow: "xtls-rprx-vision",
		TLS: subscription.TLS{
			Enabled: true, ServerName: "example.com", UTLSFingerprint: "chrome",
			Reality: subscription.Reality{Enabled: true, PublicKey: "public-key", ShortID: "0123456789abcdef"},
		},
	}
	content, _, err := BuildLatencyConfig([]subscription.Node{node}, []string{"127.0.0.1:39090"})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	outbound := config["outbounds"].([]any)[0].(map[string]any)
	tls := outbound["tls"].(map[string]any)
	reality := tls["reality"].(map[string]any)
	utls := tls["utls"].(map[string]any)
	if reality["public_key"] != "public-key" || reality["short_id"] != "0123456789abcdef" || utls["fingerprint"] != "chrome" {
		t.Fatalf("unexpected TLS config: %+v", tls)
	}
}
