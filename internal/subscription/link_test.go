package subscription

import "testing"

func TestParseNodeLinkPreservesOriginalURI(t *testing.T) {
	t.Parallel()
	original := "trojan://p%40ss@example.com:443?allowInsecure=1#Original%20Name"
	node, err := ParseNodeLink(original)
	if err != nil {
		t.Fatal(err)
	}
	if node.OriginalLink != original {
		t.Fatalf("OriginalLink = %q, want exact input", node.OriginalLink)
	}
}

func TestEncodeNodeLinkRoundTripsSupportedTypes(t *testing.T) {
	t.Parallel()
	base := Node{Name: "IPv6 Node", Server: "2001:db8::1", Port: 443}
	tests := []struct {
		name string
		node Node
	}{
		{name: "shadowsocks", node: mergeLinkTestNode(base, Node{Type: "shadowsocks", Method: "aes-128-gcm", Password: "secret"})},
		{name: "vmess", node: mergeLinkTestNode(base, Node{Type: "vmess", UUID: "11111111-1111-4111-8111-111111111111", Security: "auto", TLS: TLS{Enabled: true, ServerName: "vmess.example.com"}, Transport: Transport{Type: "ws", Path: "/ws", Headers: map[string]string{"Host": "cdn.example.com"}}})},
		{name: "socks", node: mergeLinkTestNode(base, Node{Type: "socks", Username: "user", Password: "p@ss"})},
		{name: "http", node: mergeLinkTestNode(base, Node{Type: "http", Username: "user", Password: "p@ss", TLS: TLS{Enabled: true, ServerName: "http.example.com"}})},
		{name: "trojan", node: mergeLinkTestNode(base, Node{Type: "trojan", Password: "p@ss", TLS: TLS{Enabled: true, ServerName: "trojan.example.com"}})},
		{name: "vless", node: mergeLinkTestNode(base, Node{Type: "vless", UUID: "22222222-2222-4222-8222-222222222222", Flow: "xtls-rprx-vision", TLS: TLS{Enabled: true, ServerName: "vless.example.com", Reality: Reality{Enabled: true, PublicKey: "public-key", ShortID: "abcd"}}})},
		{name: "hysteria2", node: mergeLinkTestNode(base, Node{Type: "hysteria2", Password: "secret", Obfs: "salamander", ObfsPassword: "obfs-secret", TLS: TLS{Enabled: true, ServerName: "hy.example.com"}})},
		{name: "tuic", node: mergeLinkTestNode(base, Node{Type: "tuic", UUID: "33333333-3333-4333-8333-333333333333", Password: "secret", CongestionControl: "bbr", UDPRelayMode: "native", TLS: TLS{Enabled: true, ServerName: "tuic.example.com"}})},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			link, err := EncodeNodeLink(test.node)
			if err != nil {
				t.Fatalf("EncodeNodeLink() error = %v", err)
			}
			parsed, err := ParseNodeLink(link)
			if err != nil {
				t.Fatalf("ParseNodeLink(generated) error = %v", err)
			}
			if parsed.Type != test.node.Type || parsed.Server != test.node.Server || parsed.Port != test.node.Port || parsed.Name != test.node.Name {
				t.Fatalf("round trip identity = %#v, want type=%q server=%q port=%d name=%q", parsed, test.node.Type, test.node.Server, test.node.Port, test.node.Name)
			}
		})
	}
}

func mergeLinkTestNode(base, override Node) Node {
	override.Name = base.Name
	override.Server = base.Server
	override.Port = base.Port
	return override
}
