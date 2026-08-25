package singbox

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"sing-box-webui/internal/dnsprofile"
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
		_, hasDNS := config["dns"]
		if hasDNS != (mode == ModeTUN) {
			t.Fatalf("dns presence for %s = %t, want %t", mode, hasDNS, mode == ModeTUN)
		}
		if mode == ModeTUN {
			inbound := config["inbounds"].([]any)[0].(map[string]any)
			addresses := inbound["address"].([]any)
			if len(addresses) != 2 || addresses[0] != "198.20.0.1/30" || addresses[1] != "fdfe:dcba:9876::1/126" {
				t.Fatalf("unexpected TUN addresses: %+v", addresses)
			}
			dns := config["dns"].(map[string]any)
			if dns["final"] != "dns-google" || dns["strategy"] != "prefer_ipv4" {
				t.Fatalf("unexpected TUN DNS config: %+v", dns)
			}
			rules := config["route"].(map[string]any)["rules"].([]any)
			if len(rules) == 0 || rules[0].(map[string]any)["port"] != float64(53) || rules[0].(map[string]any)["action"] != "hijack-dns" {
				t.Fatalf("unexpected TUN DNS route rule: %+v", rules)
			}
		}
	}
}

func TestBuildConfigRejectsOverlappingTUNAndFakeIPRanges(t *testing.T) {
	t.Parallel()
	err := validateTUNProfileAddress(ModeTUN, dnsprofile.Profile{
		Servers: []dnsprofile.Server{{Tag: "dns", Type: "udp", Server: "8.8.8.8"}},
		Final:   "dns", Strategy: dnsprofile.StrategyPreferIPv4,
		FakeIP: dnsprofile.FakeIP{Enabled: true, Inet4Range: "198.18.0.0/15", Inet6Range: "fc00::/18"},
	}, "198.18.0.1/30")
	if err == nil || !strings.Contains(err.Error(), "overlaps Fake IP") {
		t.Fatalf("overlap error = %v", err)
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
	if selector["type"] != "selector" || selector["tag"] != "proxy" || selector["default"] != "block" {
		t.Fatalf("unexpected selector outbound: %+v", selector)
	}
	experimental := config["experimental"].(map[string]any)
	clashAPI := experimental["clash_api"].(map[string]any)
	if clashAPI["external_controller"] != "127.0.0.1:39092" || clashAPI["secret"] != "test-secret" {
		t.Fatalf("unexpected clash API: %+v", clashAPI)
	}

	tunContent, err := BuildPoolConfig(nodes, ModeTUN, 2080, URLTestOptions{
		URL: "https://www.gstatic.com/generate_204", Interval: time.Minute, Tolerance: 80,
		IdleTimeout: 10 * time.Minute, InterruptExistingConnections: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var tunConfig map[string]any
	if err := json.Unmarshal(tunContent, &tunConfig); err != nil {
		t.Fatal(err)
	}
	if _, ok := tunConfig["dns"]; !ok {
		t.Fatal("TUN pool config is missing DNS settings")
	}
	tunInbound := tunConfig["inbounds"].([]any)[0].(map[string]any)
	addresses := tunInbound["address"].([]any)
	if len(addresses) != 2 || addresses[1] != "fdfe:dcba:9876::1/126" {
		t.Fatalf("unexpected TUN pool addresses: %+v", addresses)
	}
}

func TestBuildSelectableConfigCompilesRootAndPoolSelectors(t *testing.T) {
	t.Parallel()
	nodes := []RuntimeNodeTarget{
		{Tag: "node-000", Node: subscription.Node{Type: "shadowsocks", Server: "1.1.1.1", Port: 443, Method: "aes-128-gcm", Password: "one"}},
		{Tag: "node-001", Node: subscription.Node{Type: "trojan", Server: "8.8.8.8", Port: 443, Password: "two", TLS: subscription.TLS{Enabled: true}}},
	}
	pools := []RuntimePoolTarget{{
		Tag: "pool-000", AutoTag: "pool-auto-000", NodeTags: []string{"node-000", "node-001"},
		Options: URLTestOptions{URL: "https://www.gstatic.com/generate_204", Interval: time.Minute, Tolerance: 80, IdleTimeout: 10 * time.Minute},
	}}
	content, err := BuildSelectableConfig(nodes, pools, nil, nil, "pool-000", ModeSystemProxy, 2080, nil,
		ControllerOptions{Address: "127.0.0.1:39092", Secret: "secret"}, dnsprofile.DefaultProfile(), false)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	outbounds := config["outbounds"].([]any)
	if len(outbounds) != 7 {
		t.Fatalf("outbounds = %d, want 7", len(outbounds))
	}
	poolSelector := outbounds[3].(map[string]any)
	if poolSelector["tag"] != "pool-000" || poolSelector["default"] != "block" {
		t.Fatalf("pool selector = %#v", poolSelector)
	}
	rootSelector := outbounds[4].(map[string]any)
	if rootSelector["tag"] != "proxy" || rootSelector["default"] != "pool-000" || rootSelector["interrupt_exist_connections"] != false {
		t.Fatalf("root selector = %#v", rootSelector)
	}
}

func TestBuildSelectableConfigCompilesNodeAndPoolChains(t *testing.T) {
	t.Parallel()
	nodes := []RuntimeNodeTarget{
		{Tag: "node-entry-1", Node: subscription.Node{Name: "Entry one", Type: "shadowsocks", Server: "1.1.1.1", Port: 443, Method: "aes-128-gcm", Password: "one"}},
		{Tag: "node-entry-2", Node: subscription.Node{Name: "Entry two", Type: "trojan", Server: "8.8.8.8", Port: 443, Password: "two"}},
		{Tag: "node-exit", Node: subscription.Node{Name: "Exit", Type: "shadowsocks", Server: "9.9.9.9", Port: 443, Method: "aes-128-gcm", Password: "exit"}},
	}
	options := URLTestOptions{Interval: time.Minute, Tolerance: 80, IdleTimeout: 10 * time.Minute}
	chains := []RuntimeChainTarget{
		{Tag: "chain-node", ExitTag: "node-exit", Members: []RuntimeNodeTarget{{Tag: "chain-node", Node: nodes[0].Node}}},
		{Tag: "chain-pool", AutoTag: "chain-pool-auto", ExitTag: "node-exit", Options: &options, Members: []RuntimeNodeTarget{
			{Tag: "chain-member-1", Node: nodes[0].Node}, {Tag: "chain-member-2", Node: nodes[1].Node},
		}},
	}
	content, err := BuildSelectableConfig(nodes, nil, chains, nil, "chain-pool", ModeSystemProxy, 2080, nil,
		ControllerOptions{Address: "127.0.0.1:39092", Secret: "secret"}, dnsprofile.DefaultProfile(), false)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	outbounds := config["outbounds"].([]any)
	byTag := make(map[string]map[string]any, len(outbounds))
	for _, raw := range outbounds {
		outbound := raw.(map[string]any)
		byTag[outbound["tag"].(string)] = outbound
	}
	for _, tag := range []string{"chain-node", "chain-member-1", "chain-member-2"} {
		entryTag := tag + "-entry"
		if byTag[tag]["detour"] != entryTag {
			t.Fatalf("chain outbound %q detour = %#v, want %q", tag, byTag[tag]["detour"], entryTag)
		}
		if byTag[tag]["server"] != "9.9.9.9" {
			t.Fatalf("chain outbound %q server = %#v, want exit server", tag, byTag[tag]["server"])
		}
		if byTag[entryTag]["server"] == "9.9.9.9" {
			t.Fatalf("chain entry outbound %q unexpectedly uses exit server", entryTag)
		}
	}
	if byTag["chain-pool"]["type"] != "selector" || byTag["chain-pool-auto"]["type"] != "urltest" {
		t.Fatalf("pool chain selectors are missing: %+v", byTag)
	}
	rootTargets := byTag["proxy"]["outbounds"].([]any)
	if !containsAnyString(rootTargets, "chain-node") || !containsAnyString(rootTargets, "chain-pool") {
		t.Fatalf("root selector does not contain chain targets: %+v", rootTargets)
	}
}

func TestBuildChainLatencyConfigUsesEntryBeforeExit(t *testing.T) {
	t.Parallel()
	entry := subscription.Node{Name: "Entry", Type: "shadowsocks", Server: "1.1.1.1", Port: 443, Method: "aes-128-gcm", Password: "entry"}
	exit := subscription.Node{Name: "Exit", Type: "trojan", Server: "9.9.9.9", Port: 443, Password: "exit"}
	content, tags, err := BuildChainLatencyConfig([]subscription.Node{entry}, exit, []string{"127.0.0.1:39001"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0] != "chain-probe-000" {
		t.Fatalf("unexpected probe tags: %+v", tags)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	byTag := make(map[string]map[string]any)
	for _, raw := range config["outbounds"].([]any) {
		outbound := raw.(map[string]any)
		byTag[outbound["tag"].(string)] = outbound
	}
	if byTag["chain-probe-000"]["server"] != "9.9.9.9" || byTag["chain-probe-000"]["detour"] != "chain-probe-entry-000" {
		t.Fatalf("unexpected chain exit outbound: %+v", byTag["chain-probe-000"])
	}
	if byTag["chain-probe-entry-000"]["server"] != "1.1.1.1" {
		t.Fatalf("unexpected chain entry outbound: %+v", byTag["chain-probe-entry-000"])
	}
}

func TestBuildSelectableConfigCompilesProxyChannels(t *testing.T) {
	t.Parallel()
	nodes := []RuntimeNodeTarget{{
		Tag: "node-000", Node: subscription.Node{Type: "shadowsocks", Server: "1.1.1.1", Port: 443, Method: "aes-128-gcm", Password: "one"},
	}}
	channels := []RuntimeChannelTarget{
		{Tag: "channel-socks", Protocol: "socks5", Listen: "127.0.0.1", Port: 1080, OutboundTag: "node-000"},
		{Tag: "channel-https", Protocol: "https", Listen: "0.0.0.0", Port: 18443, Username: "app", Password: "secret", OutboundTag: "node-000", CertificatePath: "/tmp/channel.pem", KeyPath: "/tmp/channel-key.pem"},
	}
	content, err := BuildSelectableConfig(nodes, nil, nil, channels, "node-000", ModeSystemProxy, 2080, nil,
		ControllerOptions{}, dnsprofile.DefaultProfile(), false)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	inbounds := config["inbounds"].([]any)
	if len(inbounds) != 3 {
		t.Fatalf("inbounds = %d, want 3", len(inbounds))
	}
	httpsInbound := inbounds[2].(map[string]any)
	if httpsInbound["type"] != "http" || httpsInbound["listen"] != "0.0.0.0" {
		t.Fatalf("unexpected HTTPS inbound: %+v", httpsInbound)
	}
	tls := httpsInbound["tls"].(map[string]any)
	if tls["certificate_path"] != "/tmp/channel.pem" || tls["key_path"] != "/tmp/channel-key.pem" {
		t.Fatalf("unexpected HTTPS TLS config: %+v", tls)
	}
	rules := config["route"].(map[string]any)["rules"].([]any)
	for index, tag := range []string{"channel-socks", "channel-https"} {
		rule := rules[index].(map[string]any)
		if rule["outbound"] != "node-000" || rule["inbound"].([]any)[0] != tag {
			t.Fatalf("unexpected channel route rule: %+v", rule)
		}
	}
}

func containsAnyString(values []any, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestBuildConfigCustomDNSProfile(t *testing.T) {
	t.Parallel()
	node := subscription.Node{Type: "shadowsocks", Server: "1.1.1.1", Port: 443, Method: "aes-128-gcm", Password: "secret"}
	port := 853
	profile := dnsprofile.Profile{
		Servers: []dnsprofile.Server{
			{Tag: "dns-local", Type: "udp", Server: "119.29.29.29"},
			{Tag: "dns-remote", Type: "https", Server: "dns.google", Port: &port},
		},
		Final:    "dns-remote",
		Strategy: dnsprofile.StrategyPreferIPv6,
	}
	content, err := BuildConfigWithController(node, ModeTUN, 2080, nil, ControllerOptions{}, profile, false)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	dns := config["dns"].(map[string]any)
	if dns["final"] != "dns-remote" || dns["strategy"] != "prefer_ipv6" {
		t.Fatalf("unexpected DNS settings: %+v", dns)
	}
	servers := dns["servers"].([]any)
	if len(servers) != 3 {
		t.Fatalf("expected bootstrap server plus 2 configured servers, got %+v", servers)
	}
	byTag := make(map[string]map[string]any, len(servers))
	for _, item := range servers {
		server := item.(map[string]any)
		byTag[server["tag"].(string)] = server
	}
	bootstrap := byTag["dns-bootstrap"]
	if bootstrap == nil || bootstrap["type"] != "udp" || bootstrap["server"] != "223.5.5.5" {
		t.Fatalf("missing bootstrap server: %+v", servers)
	}
	plain := byTag["dns-local"]
	if plain == nil || plain["server"] != "119.29.29.29" {
		t.Fatalf("unexpected plain server: %+v", plain)
	}
	remote := byTag["dns-remote"]
	resolver := remote["domain_resolver"].(map[string]any)
	if remote["server"] != "dns.google" || remote["server_port"] != float64(853) || resolver["server"] != "dns-bootstrap" {
		t.Fatalf("domain server missing bootstrap resolver: %+v", remote)
	}
	route := config["route"].(map[string]any)
	defaultResolver := route["default_domain_resolver"].(map[string]any)
	if defaultResolver["server"] != "dns-bootstrap" {
		t.Fatalf("unexpected default domain resolver: %+v", route)
	}
}

func TestBuildConfigAvoidsProxiedDNSBootstrapCycle(t *testing.T) {
	t.Parallel()
	node := subscription.Node{
		Type: "vless", Server: "entry.example.com", Port: 443, UUID: "81cb065d-0bce-42c5-a8ce-e4b1ac1c98af",
		TLS: subscription.TLS{Enabled: true, ServerName: "entry.example.com"},
	}
	profile := dnsprofile.Profile{
		Servers: []dnsprofile.Server{{Tag: "dns-remote", Type: "https", Server: "dns.google", Detour: "proxy"}},
		Final:   "dns-remote", Strategy: dnsprofile.StrategyPreferIPv4,
	}
	content, err := BuildConfigWithController(node, ModeTUN, 2080, nil, ControllerOptions{}, profile, false)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	dns := config["dns"].(map[string]any)
	servers := dns["servers"].([]any)
	bootstrap := servers[0].(map[string]any)
	if bootstrap["tag"] != "dns-bootstrap" || bootstrap["server"] != "223.5.5.5" {
		t.Fatalf("unexpected bootstrap DNS: %+v", bootstrap)
	}
	remote := servers[1].(map[string]any)
	if remote["detour"] != "proxy" || remote["domain_resolver"].(map[string]any)["server"] != "dns-bootstrap" {
		t.Fatalf("proxied DNS server is not independently bootstrapped: %+v", remote)
	}
	resolver := config["route"].(map[string]any)["default_domain_resolver"].(map[string]any)
	if resolver["server"] != "dns-bootstrap" {
		t.Fatalf("proxy outbounds do not use bootstrap DNS: %+v", resolver)
	}
}

func TestBuildConfigFakeIPProfile(t *testing.T) {
	t.Parallel()
	node := subscription.Node{Type: "shadowsocks", Server: "1.1.1.1", Port: 443, Method: "aes-128-gcm", Password: "secret"}
	profile := dnsprofile.Profile{
		Servers:  []dnsprofile.Server{{Tag: "dns-google", Type: "udp", Server: "8.8.8.8"}},
		Final:    "dns-google",
		Strategy: dnsprofile.StrategyPreferIPv4,
		FakeIP:   dnsprofile.FakeIP{Enabled: true, Inet4Range: "198.18.0.0/15", Inet6Range: "fc00::/18"},
	}
	content, err := BuildConfigWithController(node, ModeTUN, 2080, nil, ControllerOptions{}, profile, false)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	dns := config["dns"].(map[string]any)
	if dns["final"] != "dns-google" {
		t.Fatalf("fakeip final = %v", dns["final"])
	}
	servers := dns["servers"].([]any)
	fakeip := servers[len(servers)-1].(map[string]any)
	if fakeip["type"] != "fakeip" || fakeip["tag"] != "dns-google-fakeip" || fakeip["inet4_range"] != "198.18.0.0/15" {
		t.Fatalf("unexpected fakeip server: %+v", fakeip)
	}
	rules := dns["rules"].([]any)
	queryRule := rules[0].(map[string]any)
	if queryRule["server"] != "dns-google-fakeip" {
		t.Fatalf("unexpected fakeip rule: %+v", queryRule)
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

func TestBuildConfigDefaultsRealityUTLSFingerprint(t *testing.T) {
	t.Parallel()
	node := subscription.Node{
		Type: "vless", Server: "proxy.example.com", Port: 443,
		UUID: "11111111-1111-1111-1111-111111111111",
		TLS: subscription.TLS{
			Enabled: true, ServerName: "example.com",
			Reality: subscription.Reality{Enabled: true, PublicKey: "public-key"},
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
	tls := config["outbounds"].([]any)[0].(map[string]any)["tls"].(map[string]any)
	utls := tls["utls"].(map[string]any)
	if utls["enabled"] != true || utls["fingerprint"] != "chrome" {
		t.Fatalf("unexpected default Reality uTLS config: %+v", utls)
	}
}
