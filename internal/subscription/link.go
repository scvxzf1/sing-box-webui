package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const (
	NodeLinkSourceOriginal  = "original"
	NodeLinkSourceGenerated = "generated"
)

type NodeLink struct {
	Link   string `json:"link"`
	Source string `json:"source"`
}

// EncodeNodeLink creates an importable URI for nodes whose source did not
// contain a URI, such as sing-box JSON subscriptions and older persisted data.
func EncodeNodeLink(node Node) (string, error) {
	if err := ValidateNode(node); err != nil {
		return "", fmt.Errorf("invalid node configuration")
	}
	switch node.Type {
	case "shadowsocks":
		credentials := base64.RawURLEncoding.EncodeToString([]byte(node.Method + ":" + node.Password))
		return "ss://" + credentials + "@" + nodeAddress(node) + nodeFragment(node.Name), nil
	case "vmess":
		return encodeVMessLink(node)
	case "socks":
		return encodeStandardLink(node, "socks5", standardUser(node.Username, node.Password), nil), nil
	case "http":
		scheme := "http"
		if node.TLS.Enabled {
			scheme = "https"
		}
		return encodeStandardLink(node, scheme, standardUser(node.Username, node.Password), standardQuery(node, false)), nil
	case "trojan":
		return encodeStandardLink(node, "trojan", url.User(node.Password), standardQuery(node, true)), nil
	case "vless":
		query := standardQuery(node, false)
		if node.Flow != "" {
			query.Set("flow", node.Flow)
		}
		return encodeStandardLink(node, "vless", url.User(node.UUID), query), nil
	case "hysteria2":
		query := standardQuery(node, false)
		setQuery(query, "obfs", node.Obfs)
		setQuery(query, "obfs-password", node.ObfsPassword)
		setQuery(query, "congestion_control", node.CongestionControl)
		return encodeStandardLink(node, "hysteria2", url.User(node.Password), query), nil
	case "tuic":
		query := standardQuery(node, false)
		setQuery(query, "congestion_control", node.CongestionControl)
		setQuery(query, "udp_relay_mode", node.UDPRelayMode)
		return encodeStandardLink(node, "tuic", url.UserPassword(node.UUID, node.Password), query), nil
	default:
		return "", fmt.Errorf("unsupported node type")
	}
}

func encodeVMessLink(node Node) (string, error) {
	host := ""
	if node.Transport.Headers != nil {
		host = node.Transport.Headers["Host"]
	}
	path := node.Transport.Path
	if node.Transport.Type == "grpc" {
		path = node.Transport.ServiceName
	}
	payload := map[string]any{
		"v": "2", "ps": node.Name, "add": node.Server, "port": strconv.Itoa(int(node.Port)),
		"id": node.UUID, "aid": node.AlterID, "scy": node.Security, "net": node.Transport.Type,
		"host": host, "path": path, "type": "none", "tls": "", "sni": node.TLS.ServerName,
		"alpn": strings.Join(node.TLS.ALPN, ","),
	}
	if node.TLS.Enabled {
		payload["tls"] = "tls"
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode vmess link")
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(encoded), nil
}

func encodeStandardLink(node Node, scheme string, user *url.Userinfo, query url.Values) string {
	parsed := url.URL{Scheme: scheme, User: user, Host: nodeAddress(node), Fragment: node.Name}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func standardUser(username, password string) *url.Userinfo {
	if username == "" && password == "" {
		return nil
	}
	if password == "" {
		return url.User(username)
	}
	return url.UserPassword(username, password)
}

func standardQuery(node Node, tlsImplicit bool) url.Values {
	query := make(url.Values)
	if node.TLS.Reality.Enabled {
		query.Set("security", "reality")
		setQuery(query, "pbk", node.TLS.Reality.PublicKey)
		setQuery(query, "sid", node.TLS.Reality.ShortID)
	} else if node.TLS.Enabled && !tlsImplicit {
		query.Set("security", "tls")
	}
	setQuery(query, "sni", node.TLS.ServerName)
	if node.TLS.Insecure {
		query.Set("allowInsecure", "1")
	}
	if len(node.TLS.ALPN) > 0 {
		query.Set("alpn", strings.Join(node.TLS.ALPN, ","))
	}
	setQuery(query, "fp", node.TLS.UTLSFingerprint)
	setQuery(query, "type", node.Transport.Type)
	setQuery(query, "path", node.Transport.Path)
	setQuery(query, "serviceName", node.Transport.ServiceName)
	if node.Transport.Headers != nil {
		setQuery(query, "host", node.Transport.Headers["Host"])
	}
	return query
}

func setQuery(query url.Values, key, value string) {
	if value != "" {
		query.Set(key, value)
	}
}

func nodeAddress(node Node) string {
	return net.JoinHostPort(node.Server, strconv.Itoa(int(node.Port)))
}

func nodeFragment(name string) string {
	if name == "" {
		return ""
	}
	return "#" + (&url.URL{Fragment: name}).EscapedFragment()
}
