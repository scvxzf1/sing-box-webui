package proxychain

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sing-box-webui/internal/nodepool"
	"sing-box-webui/internal/subscription"
)

type EntryType string

const (
	EntryNode EntryType = "node"
	EntryPool EntryType = "pool"
)

var ErrNotFound = errors.New("proxy chain not found")

type NodeRef struct {
	SubscriptionID string `json:"subscriptionId"`
	NodeID         string `json:"nodeId"`
}

type Chain struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	EntryType   EntryType `json:"entryType"`
	EntryNode   NodeRef   `json:"entryNode,omitempty"`
	EntryPoolID string    `json:"entryPoolId,omitempty"`
	ExitNode    NodeRef   `json:"exitNode"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type CreateInput struct {
	Name        string    `json:"name"`
	EntryType   EntryType `json:"entryType"`
	EntryNode   NodeRef   `json:"entryNode,omitempty"`
	EntryPoolID string    `json:"entryPoolId,omitempty"`
	ExitNode    NodeRef   `json:"exitNode"`
}

type UpdateInput struct {
	Name        *string    `json:"name,omitempty"`
	EntryType   *EntryType `json:"entryType,omitempty"`
	EntryNode   *NodeRef   `json:"entryNode,omitempty"`
	EntryPoolID *string    `json:"entryPoolId,omitempty"`
	ExitNode    *NodeRef   `json:"exitNode,omitempty"`
}

type View struct {
	Chain
	EntryName         string `json:"entryName,omitempty"`
	EntryMemberCount  int    `json:"entryMemberCount,omitempty"`
	ExitName          string `json:"exitName,omitempty"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailableReason,omitempty"`
}

type Resolved struct {
	Chain        Chain
	EntryNode    *subscription.Node
	EntryPool    *nodepool.Pool
	EntryMembers []nodepool.Member
	EntryNodes   []subscription.Node
	ExitNode     subscription.Node
}

type Manager struct {
	mu            sync.RWMutex
	path          string
	items         []Chain
	subscriptions *subscription.Manager
	pools         *nodepool.Manager
}

func OpenManager(dataDirectory string, subscriptions *subscription.Manager, pools *nodepool.Manager) (*Manager, error) {
	if subscriptions == nil || pools == nil {
		return nil, fmt.Errorf("subscriptions and node pools are required")
	}
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create proxy chain directory: %w", err)
	}
	if err := os.Chmod(dataDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("set proxy chain directory permissions: %w", err)
	}
	manager := &Manager{
		path: filepath.Join(dataDirectory, "chains.json"), subscriptions: subscriptions, pools: pools,
	}
	if err := manager.load(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) List() []View {
	m.mu.RLock()
	items := append([]Chain(nil), m.items...)
	m.mu.RUnlock()
	views := make([]View, 0, len(items))
	for _, item := range items {
		views = append(views, m.toView(item))
	}
	return views
}

func (m *Manager) Get(id string) (View, error) {
	chain, err := m.get(id)
	if err != nil {
		return View{}, err
	}
	return m.toView(chain), nil
}

func (m *Manager) Create(input CreateInput) (View, error) {
	now := time.Now().UTC()
	chain, err := validate(Chain{
		ID: randomID(), Name: input.Name, EntryType: input.EntryType, EntryNode: input.EntryNode,
		EntryPoolID: input.EntryPoolID, ExitNode: input.ExitNode, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return View{}, err
	}
	if _, err := m.resolve(chain); err != nil {
		return View{}, err
	}
	m.mu.Lock()
	m.items = append(m.items, chain)
	if err := m.persistLocked(); err != nil {
		m.items = m.items[:len(m.items)-1]
		m.mu.Unlock()
		return View{}, err
	}
	m.mu.Unlock()
	return m.toView(chain), nil
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
	if input.EntryType != nil {
		updated.EntryType = *input.EntryType
	}
	if input.EntryNode != nil {
		updated.EntryNode = *input.EntryNode
	}
	if input.EntryPoolID != nil {
		updated.EntryPoolID = *input.EntryPoolID
	}
	if input.ExitNode != nil {
		updated.ExitNode = *input.ExitNode
	}
	updated.UpdatedAt = time.Now().UTC()
	updated, err := validate(updated)
	if err != nil {
		m.mu.Unlock()
		return View{}, err
	}
	if _, err := m.resolve(updated); err != nil {
		m.mu.Unlock()
		return View{}, err
	}
	previous := m.items[index]
	m.items[index] = updated
	if err := m.persistLocked(); err != nil {
		m.items[index] = previous
		m.mu.Unlock()
		return View{}, err
	}
	m.mu.Unlock()
	return m.toView(updated), nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.indexOf(id)
	if index < 0 {
		return ErrNotFound
	}
	previous := append([]Chain(nil), m.items...)
	m.items = append(m.items[:index], m.items[index+1:]...)
	if err := m.persistLocked(); err != nil {
		m.items = previous
		return err
	}
	return nil
}

func (m *Manager) Resolve(id string) (Resolved, error) {
	chain, err := m.get(id)
	if err != nil {
		return Resolved{}, err
	}
	return m.resolve(chain)
}

func (m *Manager) resolve(chain Chain) (Resolved, error) {
	_, exitNode, err := m.subscriptions.SelectedNode(chain.ExitNode.SubscriptionID, chain.ExitNode.NodeID)
	if err != nil {
		return Resolved{}, fmt.Errorf("exit node is unavailable: %w", err)
	}
	resolved := Resolved{Chain: chain, ExitNode: exitNode}
	switch chain.EntryType {
	case EntryNode:
		if sameNode(chain.EntryNode, chain.ExitNode) {
			return Resolved{}, fmt.Errorf("entry and exit nodes must be different")
		}
		_, entryNode, resolveErr := m.subscriptions.SelectedNode(chain.EntryNode.SubscriptionID, chain.EntryNode.NodeID)
		if resolveErr != nil {
			return Resolved{}, fmt.Errorf("entry node is unavailable: %w", resolveErr)
		}
		resolved.EntryNode = &entryNode
	case EntryPool:
		pool, members, nodes, resolveErr := m.pools.ResolveWithMembers(chain.EntryPoolID)
		if resolveErr != nil {
			return Resolved{}, fmt.Errorf("entry node pool is unavailable: %w", resolveErr)
		}
		for _, member := range members {
			if sameNode(NodeRef{SubscriptionID: member.SubscriptionID, NodeID: member.NodeID}, chain.ExitNode) {
				return Resolved{}, fmt.Errorf("entry node pool cannot contain the exit node")
			}
		}
		resolved.EntryPool, resolved.EntryMembers, resolved.EntryNodes = &pool, members, nodes
	default:
		return Resolved{}, fmt.Errorf("unsupported proxy chain entry type %q", chain.EntryType)
	}
	return resolved, nil
}

func (m *Manager) toView(chain Chain) View {
	view := View{Chain: chain}
	resolved, err := m.resolve(chain)
	if err != nil {
		view.UnavailableReason = err.Error()
		return view
	}
	view.Available = true
	view.ExitName = resolved.ExitNode.Name
	if resolved.EntryNode != nil {
		view.EntryName = resolved.EntryNode.Name
	} else if resolved.EntryPool != nil {
		view.EntryName = resolved.EntryPool.Name
		view.EntryMemberCount = len(resolved.EntryNodes)
	}
	return view
}

func validate(chain Chain) (Chain, error) {
	chain.Name = strings.TrimSpace(chain.Name)
	chain.EntryPoolID = strings.TrimSpace(chain.EntryPoolID)
	chain.EntryNode = cleanNodeRef(chain.EntryNode)
	chain.ExitNode = cleanNodeRef(chain.ExitNode)
	if chain.Name == "" || len(chain.Name) > 80 {
		return Chain{}, fmt.Errorf("proxy chain name must contain 1-80 characters")
	}
	if chain.ExitNode.SubscriptionID == "" || chain.ExitNode.NodeID == "" {
		return Chain{}, fmt.Errorf("exit node requires subscriptionId and nodeId")
	}
	switch chain.EntryType {
	case EntryNode:
		chain.EntryPoolID = ""
		if chain.EntryNode.SubscriptionID == "" || chain.EntryNode.NodeID == "" {
			return Chain{}, fmt.Errorf("entry node requires subscriptionId and nodeId")
		}
	case EntryPool:
		chain.EntryNode = NodeRef{}
		if chain.EntryPoolID == "" {
			return Chain{}, fmt.Errorf("entryPoolId is required for a node-pool chain")
		}
	default:
		return Chain{}, fmt.Errorf("entryType must be node or pool")
	}
	return chain, nil
}

func cleanNodeRef(ref NodeRef) NodeRef {
	ref.SubscriptionID = strings.TrimSpace(ref.SubscriptionID)
	ref.NodeID = strings.TrimSpace(ref.NodeID)
	return ref
}

func sameNode(left, right NodeRef) bool {
	return left.SubscriptionID == right.SubscriptionID && left.NodeID == right.NodeID
}

func (m *Manager) get(id string) (Chain, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	index := m.indexOf(id)
	if index < 0 {
		return Chain{}, ErrNotFound
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
		m.items = []Chain{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read proxy chains: %w", err)
	}
	if err := json.Unmarshal(content, &m.items); err != nil {
		return fmt.Errorf("parse proxy chains: %w", err)
	}
	for index := range m.items {
		validated, validateErr := validate(m.items[index])
		if validateErr != nil {
			return fmt.Errorf("validate stored proxy chain %q: %w", m.items[index].ID, validateErr)
		}
		m.items[index] = validated
	}
	return nil
}

func (m *Manager) persistLocked() error {
	content, err := json.MarshalIndent(m.items, "", "  ")
	if err != nil {
		return fmt.Errorf("encode proxy chains: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(m.path), ".chains-*.tmp")
	if err != nil {
		return fmt.Errorf("create proxy chain store: %w", err)
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
		return fmt.Errorf("commit proxy chains: %w", err)
	}
	committed = true
	return nil
}

func randomID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(value[:])
}
