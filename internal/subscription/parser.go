package subscription

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

var ErrNoNodes = errors.New("subscription contains no supported nodes")

type ParseResult struct {
	Nodes         []Node
	ImportedRules []ImportedRule
	Warnings      []string
}

type Parser struct{}

func (Parser) Parse(content []byte) (ParseResult, error) {
	if len(content) == 0 {
		return ParseResult{}, ErrNoNodes
	}

	decoded := bytesAsText(content)
	if !utf8.ValidString(decoded) {
		return ParseResult{}, fmt.Errorf("subscription is not valid UTF-8")
	}

	if result, ok := parseJSONSubscription(decoded); ok {
		if len(result.Nodes) == 0 {
			return result, ErrNoNodes
		}
		return deduplicate(result), nil
	}

	result := ParseResult{}
	for lineNumber, line := range strings.Split(strings.ReplaceAll(decoded, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		node, err := parseURI(line)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("line %d: %v", lineNumber+1, err))
			continue
		}
		result.Nodes = append(result.Nodes, node)
	}
	result = deduplicate(result)
	if len(result.Nodes) == 0 {
		return result, ErrNoNodes
	}
	return result, nil
}

func bytesAsText(content []byte) string {
	trimmed := strings.TrimSpace(string(content))
	if strings.Contains(trimmed, "://") || strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return trimmed
	}
	if decoded, err := decodeBase64(trimmed); err == nil && utf8.Valid(decoded) {
		return strings.TrimSpace(string(decoded))
	}
	return trimmed
}

func parseURI(raw string) (Node, error) {
	schemeEnd := strings.Index(raw, "://")
	if schemeEnd < 1 {
		return Node{}, fmt.Errorf("unsupported subscription line")
	}
	scheme := strings.ToLower(raw[:schemeEnd])
	switch scheme {
	case "ss":
		return parseShadowsocks(raw)
	case "vmess":
		return parseVMess(raw)
	case "socks", "socks5", "http", "https", "trojan", "vless", "hysteria2", "hy2", "tuic":
		return parseStandardURI(raw, scheme)
	default:
		return Node{}, fmt.Errorf("unsupported protocol %q", scheme)
	}
}

func parseStandardURI(raw, scheme string) (Node, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return Node{}, fmt.Errorf("parse %s URI: %w", scheme, err)
	}
	port, err := parsePort(parsed.Port())
	if err != nil {
		return Node{}, err
	}
	if parsed.Hostname() == "" {
		return Node{}, fmt.Errorf("server is required")
	}

	query := parsed.Query()
	node := Node{
		ID:     nodeID(raw),
		Name:   nodeName(parsed.Fragment, scheme, parsed.Hostname(), port),
		Type:   scheme,
		Server: parsed.Hostname(),
		Port:   port,
		TLS: TLS{
			Enabled:         query.Get("security") == "tls" || query.Get("security") == "reality" || scheme == "trojan" || scheme == "hysteria2" || scheme == "hy2" || scheme == "tuic",
			ServerName:      firstNonEmpty(query.Get("sni"), query.Get("serverName"), query.Get("peer")),
			Insecure:        parseBool(firstNonEmpty(query.Get("allowInsecure"), query.Get("insecure"))),
			ALPN:            splitCSV(query.Get("alpn")),
			UTLSFingerprint: firstNonEmpty(query.Get("fp"), query.Get("fingerprint")),
			Reality: Reality{
				Enabled:   query.Get("security") == "reality",
				PublicKey: firstNonEmpty(query.Get("pbk"), query.Get("public_key")),
				ShortID:   firstNonEmpty(query.Get("sid"), query.Get("short_id")),
			},
		},
		Flow:              query.Get("flow"),
		CongestionControl: query.Get("congestion_control"),
		UDPRelayMode:      query.Get("udp_relay_mode"),
		Obfs:              query.Get("obfs"),
		ObfsPassword:      query.Get("obfs-password"),
	}
	if node.TLS.ServerName == "" && node.TLS.Enabled {
		node.TLS.ServerName = node.Server
	}
	if parsed.User != nil {
		node.Username = parsed.User.Username()
		node.Password, _ = parsed.User.Password()
	}

	switch scheme {
	case "socks5":
		node.Type = "socks"
	case "https":
		node.Type = "http"
		node.TLS.Enabled = true
	case "vless":
		node.UUID = node.Username
		node.Username = ""
	case "trojan", "hysteria2", "hy2":
		node.Type = strings.TrimSuffix(scheme, "2")
		if scheme == "hysteria2" || scheme == "hy2" {
			node.Type = "hysteria2"
		}
		node.Password = node.Username
		node.Username = ""
	case "tuic":
		node.UUID = node.Username
		node.Username = ""
	}

	transportType := query.Get("type")
	if transportType == "ws" || transportType == "grpc" || transportType == "http" || transportType == "httpupgrade" {
		node.Transport.Type = transportType
		node.Transport.Path = query.Get("path")
		node.Transport.ServiceName = firstNonEmpty(query.Get("serviceName"), query.Get("service_name"))
		if host := query.Get("host"); host != "" {
			node.Transport.Headers = map[string]string{"Host": host}
		}
	}

	return node, validateNode(node)
}

func parseVMess(raw string) (Node, error) {
	payload := strings.TrimPrefix(raw, "vmess://")
	decoded, err := decodeBase64(payload)
	if err != nil {
		return Node{}, fmt.Errorf("decode vmess payload: %w", err)
	}
	var source struct {
		Name     string `json:"ps"`
		Server   string `json:"add"`
		Port     any    `json:"port"`
		UUID     string `json:"id"`
		AlterID  any    `json:"aid"`
		Security string `json:"scy"`
		Network  string `json:"net"`
		Host     string `json:"host"`
		Path     string `json:"path"`
		TLS      string `json:"tls"`
		SNI      string `json:"sni"`
		ALPN     string `json:"alpn"`
	}
	if err := json.Unmarshal(decoded, &source); err != nil {
		return Node{}, fmt.Errorf("parse vmess payload: %w", err)
	}
	port, err := parseAnyPort(source.Port)
	if err != nil {
		return Node{}, err
	}
	node := Node{
		ID:       nodeID(raw),
		Name:     firstNonEmpty(source.Name, nodeName("", "vmess", source.Server, port)),
		Type:     "vmess",
		Server:   source.Server,
		Port:     port,
		UUID:     source.UUID,
		Security: firstNonEmpty(source.Security, "auto"),
		AlterID:  parseAnyInt(source.AlterID),
		TLS: TLS{
			Enabled:    source.TLS == "tls",
			ServerName: firstNonEmpty(source.SNI, source.Host),
			ALPN:       splitCSV(source.ALPN),
		},
	}
	if source.Network == "ws" || source.Network == "grpc" || source.Network == "http" {
		node.Transport = Transport{Type: source.Network, Path: source.Path}
		if source.Network == "grpc" {
			node.Transport.ServiceName = source.Path
		} else if source.Host != "" {
			node.Transport.Headers = map[string]string{"Host": source.Host}
		}
	}
	return node, validateNode(node)
}

func parseShadowsocks(raw string) (Node, error) {
	body := strings.TrimPrefix(raw, "ss://")
	fragment := ""
	if index := strings.IndexByte(body, '#'); index >= 0 {
		fragment, _ = url.PathUnescape(body[index+1:])
		body = body[:index]
	}
	if index := strings.IndexByte(body, '?'); index >= 0 {
		body = body[:index]
	}

	var credentials, address string
	if at := strings.LastIndexByte(body, '@'); at >= 0 {
		credentials = body[:at]
		address = body[at+1:]
		if decoded, err := decodeBase64(credentials); err == nil {
			credentials = string(decoded)
		} else if unescaped, unescapeErr := url.PathUnescape(credentials); unescapeErr == nil {
			credentials = unescaped
		}
	} else {
		decoded, err := decodeBase64(body)
		if err != nil {
			return Node{}, fmt.Errorf("decode shadowsocks payload: %w", err)
		}
		decodedText := string(decoded)
		at := strings.LastIndexByte(decodedText, '@')
		if at < 0 {
			return Node{}, fmt.Errorf("shadowsocks address is missing")
		}
		credentials, address = decodedText[:at], decodedText[at+1:]
	}

	separator := strings.IndexByte(credentials, ':')
	if separator < 1 {
		return Node{}, fmt.Errorf("shadowsocks method or password is missing")
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return Node{}, fmt.Errorf("parse shadowsocks address: %w", err)
	}
	port, err := parsePort(portText)
	if err != nil {
		return Node{}, err
	}
	node := Node{
		ID:       nodeID(raw),
		Name:     nodeName(fragment, "ss", host, port),
		Type:     "shadowsocks",
		Server:   host,
		Port:     port,
		Method:   credentials[:separator],
		Password: credentials[separator+1:],
	}
	return node, validateNode(node)
}

func parseJSONSubscription(content string) (ParseResult, bool) {
	var document struct {
		Outbounds []map[string]any `json:"outbounds"`
		Route     struct {
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal([]byte(content), &document); err != nil || document.Outbounds == nil {
		return ParseResult{}, false
	}
	result := ParseResult{}
	outboundTags := make(map[string]struct{}, len(document.Outbounds))
	for index, outbound := range document.Outbounds {
		if tag, ok := outbound["tag"].(string); ok && tag != "" {
			outboundTags[tag] = struct{}{}
		}
		node, err := nodeFromOutbound(outbound)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("outbound %d: %v", index+1, err))
			continue
		}
		result.Nodes = append(result.Nodes, node)
	}
	for index, source := range document.Route.Rules {
		rule := importedRuleFromJSON(index, source, outboundTags)
		result.ImportedRules = append(result.ImportedRules, rule)
		if !rule.Supported {
			result.Warnings = append(result.Warnings, fmt.Sprintf("route rule %d: %s", index+1, rule.UnsupportedReason))
		}
	}
	return result, true
}

func importedRuleFromJSON(index int, source map[string]any, outboundTags map[string]struct{}) ImportedRule {
	encoded, _ := json.Marshal(source)
	rule := ImportedRule{Supported: true, Source: string(encoded)}
	unsupported := make([]string, 0)

	if ruleType, _ := source["type"].(string); ruleType != "" && ruleType != "default" {
		unsupported = append(unsupported, fmt.Sprintf("unsupported rule type %q", ruleType))
	}
	if inverted, _ := source["invert"].(bool); inverted {
		unsupported = append(unsupported, "inverted rules are not supported")
	}

	supportedKeys := map[string]struct{}{
		"domain": {}, "domain_suffix": {}, "domain_keyword": {}, "ip_cidr": {},
		"ip_is_private": {}, "port": {}, "port_range": {}, "process_name": {},
		"network": {}, "protocol": {},
	}
	metadataKeys := map[string]struct{}{"type": {}, "invert": {}, "action": {}, "outbound": {}}
	for key, value := range source {
		if _, metadata := metadataKeys[key]; metadata {
			continue
		}
		if _, supported := supportedKeys[key]; !supported {
			unsupported = append(unsupported, fmt.Sprintf("unsupported condition %q", key))
			continue
		}
		values, ok := importedConditionValues(key, value)
		if !ok {
			unsupported = append(unsupported, fmt.Sprintf("invalid value for %q", key))
			continue
		}
		rule.Conditions = append(rule.Conditions, ImportedRuleCondition{Type: key, Values: values})
	}
	if len(rule.Conditions) == 0 {
		unsupported = append(unsupported, "rule has no supported conditions")
	}
	sort.Slice(rule.Conditions, func(i, j int) bool { return rule.Conditions[i].Type < rule.Conditions[j].Type })

	action, _ := source["action"].(string)
	outbound, _ := source["outbound"].(string)
	switch {
	case action == "reject":
		rule.Action = "block"
	case action != "" && action != "route":
		unsupported = append(unsupported, fmt.Sprintf("unsupported action %q", action))
	case outbound == "direct":
		rule.Action = "direct"
	case outbound == "block":
		rule.Action = "block"
	case outbound != "":
		if _, exists := outboundTags[outbound]; exists {
			rule.Action = "proxy"
		} else {
			unsupported = append(unsupported, fmt.Sprintf("outbound %q is missing", outbound))
		}
	default:
		unsupported = append(unsupported, "route action has no outbound")
	}
	if rule.Action == "" {
		rule.Action = "proxy"
	}
	rule.Name = importedRuleName(index, rule)
	if len(unsupported) > 0 {
		rule.Supported = false
		rule.UnsupportedReason = strings.Join(unsupported, "; ")
	}
	return rule
}

func importedConditionValues(key string, value any) ([]string, bool) {
	if key == "ip_is_private" {
		enabled, ok := value.(bool)
		return nil, ok && enabled
	}
	items := []any{value}
	if array, ok := value.([]any); ok {
		items = array
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case string:
			if strings.TrimSpace(typed) == "" {
				return nil, false
			}
			values = append(values, typed)
		case float64:
			if key != "port" || typed < 1 || typed > 65535 || typed != float64(int(typed)) {
				return nil, false
			}
			values = append(values, strconv.Itoa(int(typed)))
		default:
			return nil, false
		}
	}
	return values, len(values) > 0
}

func importedRuleName(index int, rule ImportedRule) string {
	if len(rule.Conditions) == 0 {
		return fmt.Sprintf("订阅规则 %d", index+1)
	}
	condition := rule.Conditions[0]
	if condition.Type == "ip_is_private" {
		return fmt.Sprintf("私有网络 · %s", rule.Action)
	}
	value := ""
	if len(condition.Values) > 0 {
		value = condition.Values[0]
		if len([]rune(value)) > 32 {
			value = string([]rune(value)[:32]) + "..."
		}
	}
	return fmt.Sprintf("%s: %s · %s", condition.Type, value, rule.Action)
}

func nodeFromOutbound(outbound map[string]any) (Node, error) {
	nodeType, _ := outbound["type"].(string)
	server, _ := outbound["server"].(string)
	port, err := parseAnyPort(outbound["server_port"])
	if err != nil {
		return Node{}, err
	}
	if nodeType == "direct" || nodeType == "block" || nodeType == "selector" || nodeType == "urltest" {
		return Node{}, fmt.Errorf("outbound type %q is not a selectable node", nodeType)
	}
	encoded, _ := json.Marshal(outbound)
	node := Node{
		ID:       nodeID(string(encoded)),
		Name:     stringValue(outbound, "tag", nodeName("", nodeType, server, port)),
		Type:     nodeType,
		Server:   server,
		Port:     port,
		Username: stringValue(outbound, "username", ""),
		Password: stringValue(outbound, "password", ""),
		UUID:     stringValue(outbound, "uuid", ""),
		Method:   stringValue(outbound, "method", ""),
		Security: stringValue(outbound, "security", ""),
		Flow:     stringValue(outbound, "flow", ""),
		AlterID:  parseAnyInt(outbound["alter_id"]),
	}
	if tls, ok := outbound["tls"].(map[string]any); ok {
		node.TLS.Enabled, _ = tls["enabled"].(bool)
		node.TLS.ServerName = stringValue(tls, "server_name", "")
		node.TLS.Insecure, _ = tls["insecure"].(bool)
		node.TLS.ALPN = stringSlice(tls["alpn"])
		if utls, ok := tls["utls"].(map[string]any); ok {
			node.TLS.UTLSFingerprint = stringValue(utls, "fingerprint", "")
		}
		if reality, ok := tls["reality"].(map[string]any); ok {
			node.TLS.Reality.Enabled, _ = reality["enabled"].(bool)
			node.TLS.Reality.PublicKey = stringValue(reality, "public_key", "")
			node.TLS.Reality.ShortID = stringValue(reality, "short_id", "")
		}
	}
	if transport, ok := outbound["transport"].(map[string]any); ok {
		node.Transport.Type = stringValue(transport, "type", "")
		node.Transport.Path = stringValue(transport, "path", "")
		node.Transport.ServiceName = stringValue(transport, "service_name", "")
		if headers, ok := transport["headers"].(map[string]any); ok {
			node.Transport.Headers = make(map[string]string, len(headers))
			for key, value := range headers {
				if text, ok := value.(string); ok {
					node.Transport.Headers[key] = text
				}
			}
		}
	}
	return node, validateNode(node)
}

func validateNode(node Node) error {
	if node.Server == "" || node.Port == 0 {
		return fmt.Errorf("server and port are required")
	}
	switch node.Type {
	case "shadowsocks":
		if node.Method == "" || node.Password == "" {
			return fmt.Errorf("shadowsocks method and password are required")
		}
	case "vless", "vmess", "tuic":
		if node.UUID == "" {
			return fmt.Errorf("UUID is required")
		}
		if node.Type == "vless" && node.TLS.Reality.Enabled && node.TLS.Reality.PublicKey == "" {
			return fmt.Errorf("Reality public key is required")
		}
	case "trojan", "hysteria2":
		if node.Password == "" {
			return fmt.Errorf("password is required")
		}
	}
	return nil
}

func deduplicate(result ParseResult) ParseResult {
	seen := make(map[string]struct{}, len(result.Nodes))
	nodes := result.Nodes[:0]
	for _, node := range result.Nodes {
		if _, exists := seen[node.ID]; exists {
			continue
		}
		seen[node.ID] = struct{}{}
		nodes = append(nodes, node)
	}
	result.Nodes = nodes
	return result
}

func decodeBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	encodings := []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding}
	for _, encoding := range encodings {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("invalid base64")
}

func nodeID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}

func nodeName(fragment, protocol, server string, port uint16) string {
	if decoded, err := url.PathUnescape(fragment); err == nil && strings.TrimSpace(decoded) != "" {
		return strings.TrimSpace(decoded)
	}
	return fmt.Sprintf("%s · %s:%d", strings.ToUpper(protocol), server, port)
}

func parsePort(value string) (uint16, error) {
	port, err := strconv.ParseUint(value, 10, 16)
	if err != nil || port == 0 {
		return 0, fmt.Errorf("invalid server port %q", value)
	}
	return uint16(port), nil
}

func parseAnyPort(value any) (uint16, error) {
	switch typed := value.(type) {
	case string:
		return parsePort(typed)
	case float64:
		return parsePort(strconv.FormatInt(int64(typed), 10))
	case json.Number:
		return parsePort(typed.String())
	default:
		return 0, fmt.Errorf("server port is required")
	}
}

func parseAnyInt(value any) int {
	switch typed := value.(type) {
	case string:
		result, _ := strconv.Atoi(typed)
		return result
	case float64:
		return int(typed)
	case json.Number:
		result, _ := strconv.Atoi(typed.String())
		return result
	default:
		return 0
	}
}

func parseBool(value string) bool {
	result, _ := strconv.ParseBool(value)
	return result || value == "1"
}

func splitCSV(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func stringValue(values map[string]any, key, fallback string) string {
	if value, ok := values[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func stringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && text != "" {
			result = append(result, text)
		}
	}
	return result
}
