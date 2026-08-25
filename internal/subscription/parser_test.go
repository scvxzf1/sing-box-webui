package subscription

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestParseNodeLinkRecognizesProtocolsWithoutLeakingCredentials(t *testing.T) {
	t.Parallel()
	tests := []struct {
		link     string
		wantType string
	}{
		{link: "trojan://secret@trojan.example.com:443#Trojan", wantType: "trojan"},
		{link: "vless://11111111-1111-1111-1111-111111111111@vless.example.com:443?security=tls#VLESS", wantType: "vless"},
		{link: "socks5://user:pass@socks.example.com:1080#SOCKS", wantType: "socks"},
	}
	for _, test := range tests {
		node, err := ParseNodeLink(test.link)
		if err != nil {
			t.Fatalf("ParseNodeLink(%q) error = %v", test.wantType, err)
		}
		if node.Type != test.wantType || node.ID == "" || node.Name == "" {
			t.Fatalf("ParseNodeLink(%q) = %+v", test.wantType, node)
		}
	}

	_, err := ParseNodeLink("trojan://do-not-leak@missing-port.example.com")
	if err == nil || strings.Contains(err.Error(), "do-not-leak") {
		t.Fatalf("ParseNodeLink() error leaked credentials: %v", err)
	}
}

func TestParserParsesBase64Subscription(t *testing.T) {
	t.Parallel()
	vmessJSON := `{"v":"2","ps":"VMess Tokyo","add":"vmess.example.com","port":"443","id":"11111111-1111-1111-1111-111111111111","aid":"0","scy":"auto","net":"ws","host":"cdn.example.com","path":"/ws","tls":"tls","sni":"vmess.example.com"}`
	vmess := "vmess://" + base64.RawStdEncoding.EncodeToString([]byte(vmessJSON))
	ssCredentials := base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:secret"))
	content := fmt.Sprintf("ss://%s@ss.example.com:8388#SS%%20Tokyo\n%s\n", ssCredentials, vmess)
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	result, err := (Parser{}).Parse([]byte(encoded))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("node count = %d, want 2; warnings=%v", len(result.Nodes), result.Warnings)
	}
	if result.Nodes[0].Type != "shadowsocks" || result.Nodes[0].Name != "SS Tokyo" {
		t.Fatalf("unexpected shadowsocks node: %+v", result.Nodes[0])
	}
	if result.Nodes[1].Type != "vmess" || !result.Nodes[1].TLS.Enabled || result.Nodes[1].Transport.Type != "ws" {
		t.Fatalf("unexpected vmess node: %+v", result.Nodes[1])
	}
}

func TestParseNodeLinkAcceptsSIP002ShadowsocksWithUnpaddedUserInfo(t *testing.T) {
	t.Parallel()

	credentials := base64.RawStdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:" + strings.Repeat("a", 64)))
	node, err := ParseNodeLink("ss://" + credentials + "@107.149.30.19:8443#mg2000")
	if err != nil {
		t.Fatalf("ParseNodeLink() error = %v", err)
	}
	if node.Type != "shadowsocks" || node.Method != "chacha20-ietf-poly1305" || node.Server != "107.149.30.19" || node.Port != 8443 || node.Name != "mg2000" {
		t.Fatalf("unexpected Shadowsocks node: type=%q method=%q server=%q port=%d name=%q", node.Type, node.Method, node.Server, node.Port, node.Name)
	}
}

func TestParserSkipsUnsupportedLines(t *testing.T) {
	t.Parallel()
	content := "unsupported://value\ntrojan://password@trojan.example.com:443?security=tls#Trojan\n"
	result, err := (Parser{}).Parse([]byte(content))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(result.Nodes) != 1 || len(result.Warnings) != 1 {
		t.Fatalf("result = %+v, want one node and one warning", result)
	}
}

func TestParserSanitizesTUICUUIDWithUserSegment(t *testing.T) {
	t.Parallel()
	uuid := "46695964-31d9-47b6-94fa-bdbdc9e6db19"
	outbound := map[string]any{
		"type":        "tuic",
		"tag":         "TUIC",
		"server":      "tuic.example.com",
		"server_port": float64(21362),
		// Some subscriptions embed "uuid:userId" into the uuid slot.
		"uuid": uuid + ":" + uuid,
	}
	node, err := nodeFromOutbound(outbound)
	if err != nil {
		t.Fatalf("nodeFromOutbound() error = %v", err)
	}
	if node.UUID != uuid {
		t.Fatalf("UUID = %q, want sanitized %q", node.UUID, uuid)
	}
}

func TestParserAcceptsTUICAllowInsecureSnakeCase(t *testing.T) {
	t.Parallel()
	const uuid = "2cf8b091-58eb-40e4-8524-bc45142c400e"
	node, err := ParseNodeLink("tuic://" + uuid + ":secret@107.149.30.19:8445?congestion_control=bbr&udp_relay_mode=native&alpn=h3&sni=mg2000&allow_insecure=1#mg2000-TUIC-v5")
	if err != nil {
		t.Fatalf("ParseNodeLink() error = %v", err)
	}
	if node.Type != "tuic" || node.UUID != uuid || !node.TLS.Insecure {
		t.Fatalf("unexpected TUIC node: type=%q uuid=%q insecure=%t", node.Type, node.UUID, node.TLS.Insecure)
	}
}

func TestParserRejectsInvalidShadowsocks2022Key(t *testing.T) {
	t.Parallel()

	_, err := nodeFromOutbound(map[string]any{
		"type":        "shadowsocks",
		"tag":         "bad-ss2022",
		"server":      "1.2.3.4",
		"server_port": float64(443),
		"method":      "2022-blake3-aes-256-gcm",
		"password":    "bauhMwAMsQqSGowhSUxTeC9iTyEW2YNtWJ9v1Svj7JpZ",
	})
	if err == nil || !strings.Contains(err.Error(), "requires a base64 key of 32 bytes") {
		t.Fatalf("invalid SS2022 key error = %v", err)
	}
}

func TestParserPreservesVLESSReality(t *testing.T) {
	t.Parallel()
	raw := "vless://11111111-1111-1111-1111-111111111111@proxy.example.com:443?security=reality&sni=example.com&fp=chrome&pbk=public-key&sid=0123456789abcdef&type=tcp&flow=xtls-rprx-vision#Reality"
	result, err := (Parser{}).Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	node := result.Nodes[0]
	if !node.TLS.Reality.Enabled || node.TLS.Reality.PublicKey != "public-key" || node.TLS.Reality.ShortID != "0123456789abcdef" || node.TLS.UTLSFingerprint != "chrome" {
		t.Fatalf("Reality fields were not preserved: %+v", node.TLS)
	}
}

func TestParserImportsRouteRulesWithoutEnablingThem(t *testing.T) {
	t.Parallel()
	content := `{
  "outbounds": [
    {"type":"shadowsocks","tag":"proxy","server":"1.1.1.1","server_port":443,"method":"aes-128-gcm","password":"secret"},
    {"type":"direct","tag":"direct"}
  ],
  "route": {"rules": [
    {"domain_suffix":["example.com"],"outbound":"proxy"},
    {"rule_set":["geoip-cn"],"outbound":"direct"}
  ]}
}`
	result, err := (Parser{}).Parse([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ImportedRules) != 2 {
		t.Fatalf("rules = %#v", result.ImportedRules)
	}
	if !result.ImportedRules[0].Supported || result.ImportedRules[0].Action != "proxy" {
		t.Fatalf("supported rule = %#v", result.ImportedRules[0])
	}
	if result.ImportedRules[1].Supported || result.ImportedRules[1].UnsupportedReason == "" {
		t.Fatalf("unsupported rule = %#v", result.ImportedRules[1])
	}
}

func TestValidateSubscriptionURLRejectsLocalAddresses(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"file:///tmp/subscription", "http://127.0.0.1/sub", "http://[::1]/sub", "http://192.168.1.1/sub"} {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateSubscriptionURL(parsed); err == nil {
			t.Fatalf("validateSubscriptionURL(%q) returned nil", raw)
		}
	}
}
