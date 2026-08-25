package proxychannel

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sing-box-webui/internal/subscription"
)

type Protocol string
type Direction string

const (
	ProtocolSOCKS5   Protocol  = "socks5"
	ProtocolHTTP     Protocol  = "http"
	ProtocolHTTPS    Protocol  = "https"
	DirectionForward Direction = "forward"
	DirectionReverse Direction = "reverse"
)

var ErrNotFound = errors.New("proxy channel not found")

type NodeRef struct {
	SubscriptionID string `json:"subscriptionId"`
	NodeID         string `json:"nodeId"`
}

type Channel struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Protocol  Protocol  `json:"protocol"`
	Direction Direction `json:"direction"`
	Port      uint16    `json:"port"`
	Username  string    `json:"username,omitempty"`
	Password  string    `json:"password,omitempty"`
	Node      NodeRef   `json:"node"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type CreateInput struct {
	Name      string    `json:"name"`
	Protocol  Protocol  `json:"protocol"`
	Direction Direction `json:"direction"`
	Port      uint16    `json:"port"`
	Username  string    `json:"username,omitempty"`
	Password  string    `json:"password,omitempty"`
	Node      NodeRef   `json:"node"`
	Enabled   bool      `json:"enabled"`
}

type UpdateInput struct {
	Name      *string    `json:"name,omitempty"`
	Protocol  *Protocol  `json:"protocol,omitempty"`
	Direction *Direction `json:"direction,omitempty"`
	Port      *uint16    `json:"port,omitempty"`
	Username  *string    `json:"username,omitempty"`
	Password  *string    `json:"password,omitempty"`
	Node      *NodeRef   `json:"node,omitempty"`
	Enabled   *bool      `json:"enabled,omitempty"`
}

type View struct {
	Channel
	NodeName          string   `json:"nodeName,omitempty"`
	ListenAddress     string   `json:"listenAddress"`
	AccessAddresses   []string `json:"accessAddresses"`
	Available         bool     `json:"available"`
	UnavailableReason string   `json:"unavailableReason,omitempty"`
}

type Resolved struct {
	Channel Channel
	Node    subscription.Node
}

type Manager struct {
	mu              sync.RWMutex
	path            string
	certificatePath string
	keyPath         string
	items           []Channel
	subscriptions   *subscription.Manager
	reservedPorts   map[uint16]struct{}
	reload          func() error
}

func OpenManager(dataDirectory string, subscriptions *subscription.Manager, reservedPorts ...uint16) (*Manager, error) {
	if subscriptions == nil {
		return nil, fmt.Errorf("subscriptions are required")
	}
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create proxy channel directory: %w", err)
	}
	if err := os.Chmod(dataDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("set proxy channel directory permissions: %w", err)
	}
	manager := &Manager{
		path:            filepath.Join(dataDirectory, "channels.json"),
		certificatePath: filepath.Join(dataDirectory, "channel-certificate.pem"),
		keyPath:         filepath.Join(dataDirectory, "channel-key.pem"),
		subscriptions:   subscriptions,
		reservedPorts:   make(map[uint16]struct{}, len(reservedPorts)),
	}
	for _, port := range reservedPorts {
		if port != 0 {
			manager.reservedPorts[port] = struct{}{}
		}
	}
	if err := manager.load(); err != nil {
		return nil, err
	}
	if err := manager.ensureCertificate(); err != nil {
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

func (m *Manager) List() []View {
	m.mu.RLock()
	items := append([]Channel(nil), m.items...)
	m.mu.RUnlock()
	views := make([]View, 0, len(items))
	for _, item := range items {
		views = append(views, m.toView(item))
	}
	return views
}

func (m *Manager) Get(id string) (View, error) {
	channel, err := m.get(id)
	if err != nil {
		return View{}, err
	}
	return m.toView(channel), nil
}

func (m *Manager) Create(input CreateInput) (View, error) {
	now := time.Now().UTC()
	channel, err := validate(Channel{
		ID: randomID(), Name: input.Name, Protocol: input.Protocol, Direction: input.Direction,
		Port: input.Port, Username: input.Username, Password: input.Password, Node: input.Node,
		Enabled: input.Enabled, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return View{}, err
	}
	if _, err := m.resolve(channel); err != nil {
		return View{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validatePortLocked(channel, ""); err != nil {
		return View{}, err
	}
	m.items = append(m.items, channel)
	if err := m.persistLocked(); err != nil {
		m.items = m.items[:len(m.items)-1]
		return View{}, err
	}
	return m.toView(channel), nil
}

func (m *Manager) Update(id string, input UpdateInput) (View, error) {
	m.mu.Lock()
	index := m.indexOf(id)
	if index < 0 {
		m.mu.Unlock()
		return View{}, ErrNotFound
	}
	updated := m.items[index]
	if input.Name != nil {
		updated.Name = *input.Name
	}
	if input.Protocol != nil {
		updated.Protocol = *input.Protocol
	}
	if input.Direction != nil {
		updated.Direction = *input.Direction
	}
	if input.Port != nil {
		updated.Port = *input.Port
	}
	if input.Username != nil {
		updated.Username = *input.Username
	}
	if input.Password != nil {
		updated.Password = *input.Password
	}
	if input.Node != nil {
		updated.Node = *input.Node
	}
	if input.Enabled != nil {
		updated.Enabled = *input.Enabled
	}
	updated.UpdatedAt = time.Now().UTC()
	validated, err := validate(updated)
	if err != nil {
		m.mu.Unlock()
		return View{}, err
	}
	if err := m.validatePortLocked(validated, id); err != nil {
		m.mu.Unlock()
		return View{}, err
	}
	if _, err := m.resolve(validated); err != nil {
		m.mu.Unlock()
		return View{}, err
	}
	previous := m.items[index]
	m.items[index] = validated
	if err := m.persistLocked(); err != nil {
		m.items[index] = previous
		m.mu.Unlock()
		return View{}, err
	}
	m.mu.Unlock()
	return m.toView(validated), nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.indexOf(id)
	if index < 0 {
		return ErrNotFound
	}
	previous := append([]Channel(nil), m.items...)
	m.items = append(m.items[:index], m.items[index+1:]...)
	if err := m.persistLocked(); err != nil {
		m.items = previous
		return err
	}
	return nil
}

func (m *Manager) Resolve(id string) (Resolved, error) {
	channel, err := m.get(id)
	if err != nil {
		return Resolved{}, err
	}
	return m.resolve(channel)
}

func (m *Manager) ResolveEnabled() []Resolved {
	m.mu.RLock()
	items := append([]Channel(nil), m.items...)
	m.mu.RUnlock()
	result := make([]Resolved, 0, len(items))
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		if resolved, err := m.resolve(item); err == nil {
			result = append(result, resolved)
		}
	}
	return result
}

func (m *Manager) TLSPaths() (certificatePath, keyPath string) {
	return m.certificatePath, m.keyPath
}

func (m *Manager) Certificate() ([]byte, error) {
	return os.ReadFile(m.certificatePath)
}

func (m *Manager) resolve(channel Channel) (Resolved, error) {
	_, node, err := m.subscriptions.SelectedNode(channel.Node.SubscriptionID, channel.Node.NodeID)
	if err != nil {
		return Resolved{}, fmt.Errorf("channel node is unavailable: %w", err)
	}
	return Resolved{Channel: channel, Node: node}, nil
}

func (m *Manager) toView(channel Channel) View {
	listen := "127.0.0.1"
	accessAddresses := []string{net.JoinHostPort(listen, fmt.Sprintf("%d", channel.Port))}
	if channel.Direction == DirectionReverse {
		listen = "0.0.0.0"
		accessAddresses = localAccessAddresses(channel.Port)
	}
	view := View{
		Channel: channel, ListenAddress: net.JoinHostPort(listen, fmt.Sprintf("%d", channel.Port)),
		AccessAddresses: accessAddresses,
	}
	resolved, err := m.resolve(channel)
	if err != nil {
		view.UnavailableReason = err.Error()
		return view
	}
	view.Available = true
	view.NodeName = resolved.Node.Name
	return view
}

func localAccessAddresses(port uint16) []string {
	seen := make(map[string]struct{})
	addresses := make([]string, 0, 2)
	for _, target := range []struct {
		network string
		address string
	}{{"udp4", "1.1.1.1:53"}, {"udp6", "[2606:4700:4700::1111]:53"}} {
		connection, err := net.Dial(target.network, target.address)
		if err != nil {
			continue
		}
		local, _ := connection.LocalAddr().(*net.UDPAddr)
		_ = connection.Close()
		if local == nil || local.IP == nil || local.IP.IsLoopback() || local.IP.IsUnspecified() {
			continue
		}
		address := net.JoinHostPort(local.IP.String(), fmt.Sprintf("%d", port))
		if _, exists := seen[address]; !exists {
			seen[address] = struct{}{}
			addresses = append(addresses, address)
		}
	}
	if len(addresses) == 0 {
		for _, ip := range localInterfaceIPs() {
			if !ip.IsPrivate() || ip.IsLinkLocalUnicast() {
				continue
			}
			address := net.JoinHostPort(ip.String(), fmt.Sprintf("%d", port))
			if _, exists := seen[address]; !exists {
				seen[address] = struct{}{}
				addresses = append(addresses, address)
			}
		}
	}
	return addresses
}

func validate(channel Channel) (Channel, error) {
	channel.Name = strings.TrimSpace(channel.Name)
	channel.Username = strings.TrimSpace(channel.Username)
	channel.Node.SubscriptionID = strings.TrimSpace(channel.Node.SubscriptionID)
	channel.Node.NodeID = strings.TrimSpace(channel.Node.NodeID)
	if channel.Name == "" || len(channel.Name) > 80 {
		return Channel{}, fmt.Errorf("proxy channel name must contain 1-80 characters")
	}
	switch channel.Protocol {
	case ProtocolSOCKS5, ProtocolHTTP, ProtocolHTTPS:
	default:
		return Channel{}, fmt.Errorf("protocol must be socks5, http or https")
	}
	switch channel.Direction {
	case DirectionForward:
	case DirectionReverse:
		if channel.Username == "" || channel.Password == "" {
			return Channel{}, fmt.Errorf("reverse proxy channels require username and password authentication")
		}
	default:
		return Channel{}, fmt.Errorf("direction must be forward or reverse")
	}
	if channel.Port < 1024 {
		return Channel{}, fmt.Errorf("proxy channel port must be between 1024 and 65535")
	}
	if (channel.Username == "") != (channel.Password == "") {
		return Channel{}, fmt.Errorf("username and password must be configured together")
	}
	if len(channel.Username) > 128 || len(channel.Password) > 256 {
		return Channel{}, fmt.Errorf("proxy channel credentials are too long")
	}
	if channel.Node.SubscriptionID == "" || channel.Node.NodeID == "" {
		return Channel{}, fmt.Errorf("channel node requires subscriptionId and nodeId")
	}
	return channel, nil
}

func (m *Manager) validatePortLocked(channel Channel, excludeID string) error {
	if _, reserved := m.reservedPorts[channel.Port]; reserved {
		return fmt.Errorf("proxy channel port %d is reserved by the application", channel.Port)
	}
	for _, item := range m.items {
		if item.ID != excludeID && item.Port == channel.Port {
			return fmt.Errorf("proxy channel port %d is already in use", channel.Port)
		}
	}
	return nil
}

func (m *Manager) get(id string) (Channel, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	index := m.indexOf(id)
	if index < 0 {
		return Channel{}, ErrNotFound
	}
	return m.items[index], nil
}

func (m *Manager) indexOf(id string) int {
	for index := range m.items {
		if m.items[index].ID == id {
			return index
		}
	}
	return -1
}

func (m *Manager) load() error {
	content, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		m.items = []Channel{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read proxy channels: %w", err)
	}
	if err := json.Unmarshal(content, &m.items); err != nil {
		return fmt.Errorf("parse proxy channels: %w", err)
	}
	for index := range m.items {
		validated, validateErr := validate(m.items[index])
		if validateErr != nil {
			return fmt.Errorf("validate stored proxy channel %q: %w", m.items[index].ID, validateErr)
		}
		if err := m.validatePortLocked(validated, validated.ID); err != nil {
			return err
		}
		m.items[index] = validated
	}
	return nil
}

func (m *Manager) persistLocked() error {
	content, err := json.MarshalIndent(m.items, "", "  ")
	if err != nil {
		return fmt.Errorf("encode proxy channels: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(m.path), ".channels-*.tmp")
	if err != nil {
		return fmt.Errorf("create proxy channel store: %w", err)
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
		return err
	}
	committed = true
	return nil
}

func (m *Manager) ensureCertificate() error {
	if certificate, certErr := os.ReadFile(m.certificatePath); certErr == nil {
		if key, keyErr := os.ReadFile(m.keyPath); keyErr == nil && len(certificate) > 0 && len(key) > 0 {
			return nil
		}
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate proxy channel key: %w", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return fmt.Errorf("generate proxy channel certificate serial: %w", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "sing-box WebUI Proxy Channel"},
		NotBefore:    now.Add(-time.Hour), NotAfter: now.AddDate(10, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:        true, BasicConstraintsValid: true,
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	for _, ip := range localInterfaceIPs() {
		template.IPAddresses = append(template.IPAddresses, ip)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return fmt.Errorf("create proxy channel certificate: %w", err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	if err := writePrivateFile(m.certificatePath, certificatePEM); err != nil {
		return err
	}
	if err := writePrivateFile(m.keyPath, keyPEM); err != nil {
		return err
	}
	return nil
}

func localInterfaceIPs() []net.IP {
	addresses, _ := net.InterfaceAddrs()
	result := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && !ip.IsLoopback() && !ip.IsUnspecified() {
			result = append(result, ip)
		}
	}
	return result
}

func writePrivateFile(path string, content []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".certificate-*.tmp")
	if err != nil {
		return err
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
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	committed = true
	return nil
}

func randomID() string {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value)
}
