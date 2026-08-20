package routing

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sing-box-webui/internal/events"
	"sing-box-webui/internal/subscription"
)

var ErrNotFound = errors.New("rule not found")
var ErrPoolNotFound = errors.New("rule pool not found")

var supportedConditions = map[string]struct{}{
	"domain": {}, "domain_suffix": {}, "domain_keyword": {}, "ip_cidr": {},
	"ip_is_private": {}, "port": {}, "port_range": {}, "process_name": {},
	"network": {}, "protocol": {},
}

type Manager struct {
	mu       sync.RWMutex
	path     string
	poolPath string
	rules    []Rule
	pools    []RulePool
	events   *events.Broker
	reload   func() error
}

func (m *Manager) SetReload(reload func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reload = reload
}

func OpenManager(dataDirectory string, broker *events.Broker) (*Manager, error) {
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create routing data directory: %w", err)
	}
	manager := &Manager{
		path: filepath.Join(dataDirectory, "rules.json"), poolPath: filepath.Join(dataDirectory, "rule-pools.json"),
		events: broker,
	}
	if err := manager.load(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) List() []Rule {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Rule, 0, len(m.rules)+1)
	for _, rule := range m.rules {
		result = append(result, cloneRule(rule))
	}
	sortRules(result)
	return append(result, builtinGlobalRule())
}

func (m *Manager) Create(input CreateInput) (Rule, error) {
	name, conditions, action, err := validateRuleInput(input.Name, input.Conditions, input.Action)
	if err != nil {
		return Rule{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rule := Rule{
		ID: randomID(), Name: name, Enabled: input.Enabled, Origin: OriginManual,
		Conditions: conditions, Action: action, Supported: true, Position: m.nextManualPositionLocked(),
	}
	m.rules = append(m.rules, rule)
	if err := m.persistLocked(); err != nil {
		m.rules = m.rules[:len(m.rules)-1]
		return Rule{}, err
	}
	m.publish("rule.created", map[string]string{"ruleId": rule.ID})
	return cloneRule(rule), nil
}

func (m *Manager) Update(id string, input UpdateInput) (Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.indexOfLocked(id)
	if index < 0 {
		return Rule{}, ErrNotFound
	}
	current := m.rules[index]
	previous := current
	if current.Origin == OriginSubscription {
		if input.Name != nil || input.Conditions != nil || input.Action != nil {
			return Rule{}, fmt.Errorf("subscription rules only support enable or disable")
		}
		if input.Enabled != nil && *input.Enabled && !current.Supported {
			return Rule{}, fmt.Errorf("unsupported subscription rule cannot be enabled: %s", current.UnsupportedReason)
		}
		if input.Enabled != nil {
			current.Enabled = *input.Enabled
		}
	} else if current.Origin == OriginManual {
		name, conditions, action := current.Name, current.Conditions, current.Action
		if input.Name != nil {
			name = *input.Name
		}
		if input.Conditions != nil {
			conditions = *input.Conditions
		}
		if input.Action != nil {
			action = *input.Action
		}
		var err error
		name, conditions, action, err = validateRuleInput(name, conditions, action)
		if err != nil {
			return Rule{}, err
		}
		current.Name, current.Conditions, current.Action = name, conditions, action
		if input.Enabled != nil {
			current.Enabled = *input.Enabled
		}
	} else {
		return Rule{}, fmt.Errorf("built-in rule cannot be modified")
	}
	m.rules[index] = current
	if err := m.persistLocked(); err != nil {
		m.rules[index] = previous
		return Rule{}, err
	}
	m.publish("rule.updated", map[string]string{"ruleId": id})
	return cloneRule(current), nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.indexOfLocked(id)
	if index < 0 {
		return ErrNotFound
	}
	if m.rules[index].Origin != OriginManual {
		return fmt.Errorf("only manual rules can be deleted")
	}
	previous := cloneRules(m.rules)
	m.rules = append(m.rules[:index], m.rules[index+1:]...)
	m.normalizeManualPositionsLocked()
	if err := m.persistLocked(); err != nil {
		m.rules = previous
		return err
	}
	m.publish("rule.deleted", map[string]string{"ruleId": id})
	return nil
}

func (m *Manager) Reorder(ids []string) ([]Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	manual := make(map[string]int)
	for index, rule := range m.rules {
		if rule.Origin == OriginManual {
			manual[rule.ID] = index
		}
	}
	if len(ids) != len(manual) {
		return nil, fmt.Errorf("order must include every manual rule exactly once")
	}
	previous := cloneRules(m.rules)
	seen := make(map[string]struct{}, len(ids))
	for position, id := range ids {
		index, exists := manual[id]
		if !exists {
			return nil, fmt.Errorf("order contains an unknown manual rule")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("order contains a duplicate rule")
		}
		seen[id] = struct{}{}
		m.rules[index].Position = position
	}
	if err := m.persistLocked(); err != nil {
		m.rules = previous
		return nil, err
	}
	m.publish("rules.reordered", map[string]int{"count": len(ids)})
	return m.listLocked(), nil
}

func (m *Manager) SyncSubscriptionRules(subscriptionID, subscriptionName string, imported []subscription.ImportedRule) error {
	m.mu.Lock()
	previousRules := cloneRules(m.rules)
	existing := make(map[string]Rule)
	kept := m.rules[:0]
	for _, rule := range m.rules {
		if rule.Origin == OriginSubscription && rule.SubscriptionID == subscriptionID {
			existing[rule.ID] = rule
			continue
		}
		kept = append(kept, rule)
	}
	m.rules = kept
	occurrences := make(map[string]int)
	for position, source := range imported {
		occurrence := occurrences[source.Source]
		occurrences[source.Source]++
		id := subscriptionRuleID(subscriptionID, source.Source, occurrence)
		enabled := false
		if previous, ok := existing[id]; ok && source.Supported {
			enabled = previous.Enabled
		}
		conditions := make([]Condition, len(source.Conditions))
		for index, condition := range source.Conditions {
			conditions[index] = Condition{Type: condition.Type, Values: append([]string(nil), condition.Values...)}
		}
		m.rules = append(m.rules, Rule{
			ID: id, Name: source.Name, Enabled: enabled, Origin: OriginSubscription,
			SubscriptionID: subscriptionID, SubscriptionName: subscriptionName,
			Conditions: conditions, Action: Action(source.Action), Supported: source.Supported,
			UnsupportedReason: source.UnsupportedReason, Source: source.Source, Position: position,
		})
	}
	if err := m.persistLocked(); err != nil {
		m.rules = previousRules
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	m.publish("subscription.rules.synced", map[string]any{"subscriptionId": subscriptionID, "ruleCount": len(imported)})
	return nil
}

func (m *Manager) DeleteSubscriptionRules(subscriptionID string) error {
	m.mu.Lock()
	previousRules := cloneRules(m.rules)
	kept := m.rules[:0]
	for _, rule := range m.rules {
		if rule.Origin != OriginSubscription || rule.SubscriptionID != subscriptionID {
			kept = append(kept, rule)
		}
	}
	m.rules = kept
	if err := m.persistLocked(); err != nil {
		m.rules = previousRules
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	return nil
}

// ReconcileSubscriptionRules removes imported rules whose owning
// subscription no longer exists. This repairs interrupted cross-store deletes
// before the runtime can compile stale rules.
func (m *Manager) ReconcileSubscriptionRules(subscriptionIDs []string) error {
	valid := make(map[string]struct{}, len(subscriptionIDs))
	for _, id := range subscriptionIDs {
		valid[id] = struct{}{}
	}
	m.mu.Lock()
	previousRules := cloneRules(m.rules)
	kept := m.rules[:0]
	changed := false
	for _, rule := range m.rules {
		if rule.Origin == OriginSubscription {
			if _, exists := valid[rule.SubscriptionID]; !exists {
				changed = true
				continue
			}
		}
		kept = append(kept, rule)
	}
	if !changed {
		m.mu.Unlock()
		return nil
	}
	m.rules = kept
	if err := m.persistLocked(); err != nil {
		m.rules = previousRules
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	m.publish("subscription.rules.reconciled", map[string]int{"subscriptionCount": len(subscriptionIDs)})
	return nil
}

func (m *Manager) ReloadRules() error {
	m.mu.RLock()
	reload := m.reload
	m.mu.RUnlock()
	if reload != nil {
		return reload()
	}
	return nil
}

func (m *Manager) Compiled() ([]map[string]any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	manual := make([]Rule, 0, len(m.rules))
	subscriptionRules := make([]Rule, 0, len(m.rules))
	for _, rule := range m.rules {
		if !rule.Enabled {
			continue
		}
		if rule.Origin == OriginManual {
			manual = append(manual, cloneRule(rule))
		} else {
			subscriptionRules = append(subscriptionRules, cloneRule(rule))
		}
	}
	sortRules(manual)
	sortRules(subscriptionRules)
	rules := make([]Rule, 0, len(manual)+len(subscriptionRules))
	rules = append(rules, manual...)
	pools := cloneRulePools(m.pools)
	sortRulePools(pools)
	for _, pool := range pools {
		if !pool.Enabled {
			continue
		}
		for _, poolRule := range pool.Rules {
			if !poolRule.Enabled {
				continue
			}
			rules = append(rules, Rule{
				ID: poolRule.ID, Name: poolRule.Name, Enabled: true, Origin: OriginManual,
				Conditions: poolRule.Conditions, Action: poolRule.Action, Supported: true, Position: poolRule.Position,
			})
		}
	}
	rules = append(rules, subscriptionRules...)
	compiled := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		if !rule.Supported {
			return nil, fmt.Errorf("enabled rule %q is unsupported: %s", rule.Name, rule.UnsupportedReason)
		}
		value, err := compileRule(rule)
		if err != nil {
			return nil, fmt.Errorf("compile rule %q: %w", rule.Name, err)
		}
		compiled = append(compiled, value)
	}
	return compiled, nil
}

func compileRule(rule Rule) (map[string]any, error) {
	result := make(map[string]any)
	for _, condition := range rule.Conditions {
		switch condition.Type {
		case "ip_is_private":
			result[condition.Type] = true
		case "port":
			ports := make([]int, 0, len(condition.Values))
			for _, value := range condition.Values {
				port, err := strconv.Atoi(value)
				if err != nil {
					return nil, fmt.Errorf("invalid port %q", value)
				}
				ports = append(ports, port)
			}
			result[condition.Type] = ports
		default:
			result[condition.Type] = append([]string(nil), condition.Values...)
		}
	}
	result["action"] = "route"
	result["outbound"] = string(rule.Action)
	return result, nil
}

func validateRuleInput(name string, conditions []Condition, action Action) (string, []Condition, Action, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 {
		return "", nil, "", fmt.Errorf("rule name must contain 1-80 characters")
	}
	if action != ActionProxy && action != ActionDirect && action != ActionBlock {
		return "", nil, "", fmt.Errorf("rule action must be proxy, direct, or block")
	}
	if len(conditions) == 0 || len(conditions) > 8 {
		return "", nil, "", fmt.Errorf("rule must contain 1-8 conditions")
	}
	normalized := make([]Condition, 0, len(conditions))
	seenTypes := make(map[string]struct{})
	for _, condition := range conditions {
		condition.Type = strings.TrimSpace(condition.Type)
		if _, ok := supportedConditions[condition.Type]; !ok {
			return "", nil, "", fmt.Errorf("unsupported condition type %q", condition.Type)
		}
		if _, duplicate := seenTypes[condition.Type]; duplicate {
			return "", nil, "", fmt.Errorf("condition type %q is duplicated", condition.Type)
		}
		seenTypes[condition.Type] = struct{}{}
		values := make([]string, 0, len(condition.Values))
		seenValues := make(map[string]struct{})
		for _, value := range condition.Values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if len(value) > 512 {
				return "", nil, "", fmt.Errorf("condition value is too long")
			}
			if _, exists := seenValues[value]; !exists {
				values = append(values, value)
				seenValues[value] = struct{}{}
			}
		}
		if condition.Type != "ip_is_private" && len(values) == 0 {
			return "", nil, "", fmt.Errorf("condition %q requires at least one value", condition.Type)
		}
		if len(values) > 256 {
			return "", nil, "", fmt.Errorf("condition %q has more than 256 values", condition.Type)
		}
		if condition.Type == "ip_is_private" {
			values = nil
		}
		if condition.Type == "port" {
			for _, value := range values {
				port, err := strconv.Atoi(value)
				if err != nil || port < 1 || port > 65535 {
					return "", nil, "", fmt.Errorf("invalid port %q", value)
				}
			}
		}
		if condition.Type == "port_range" {
			for _, value := range values {
				parts := strings.Split(value, ":")
				if len(parts) != 2 {
					return "", nil, "", fmt.Errorf("invalid port range %q", value)
				}
				start, startErr := strconv.Atoi(parts[0])
				end, endErr := strconv.Atoi(parts[1])
				if startErr != nil || endErr != nil || start < 1 || end > 65535 || start > end {
					return "", nil, "", fmt.Errorf("invalid port range %q", value)
				}
			}
		}
		if condition.Type == "ip_cidr" {
			for _, value := range values {
				if net.ParseIP(value) == nil {
					if _, _, err := net.ParseCIDR(value); err != nil {
						return "", nil, "", fmt.Errorf("invalid IP or CIDR %q", value)
					}
				}
			}
		}
		if condition.Type == "network" {
			for _, value := range values {
				if value != "tcp" && value != "udp" {
					return "", nil, "", fmt.Errorf("network must be tcp or udp")
				}
			}
		}
		normalized = append(normalized, Condition{Type: condition.Type, Values: values})
	}
	return name, normalized, action, nil
}

func (m *Manager) load() error {
	content, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		m.rules = []Rule{}
	} else if err != nil {
		return fmt.Errorf("read rules: %w", err)
	} else if err := json.Unmarshal(content, &m.rules); err != nil {
		return fmt.Errorf("parse rules: %w", err)
	}
	content, err = os.ReadFile(m.poolPath)
	if errors.Is(err, os.ErrNotExist) {
		m.pools = []RulePool{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read rule pools: %w", err)
	}
	if err := json.Unmarshal(content, &m.pools); err != nil {
		return fmt.Errorf("parse rule pools: %w", err)
	}
	return nil
}

func (m *Manager) persistLocked() error {
	content, err := json.MarshalIndent(m.rules, "", "  ")
	if err != nil {
		return fmt.Errorf("encode rules: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(m.path), ".rules-*.tmp")
	if err != nil {
		return fmt.Errorf("create rules store: %w", err)
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
		return fmt.Errorf("commit rules: %w", err)
	}
	committed = true
	return nil
}

func (m *Manager) listLocked() []Rule {
	result := make([]Rule, 0, len(m.rules)+1)
	for _, rule := range m.rules {
		result = append(result, cloneRule(rule))
	}
	sortRules(result)
	return append(result, builtinGlobalRule())
}

func sortRules(rules []Rule) {
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Origin != rules[j].Origin {
			order := map[Origin]int{OriginManual: 0, OriginSubscription: 1, OriginBuiltin: 2}
			return order[rules[i].Origin] < order[rules[j].Origin]
		}
		if rules[i].SubscriptionID != rules[j].SubscriptionID {
			return rules[i].SubscriptionID < rules[j].SubscriptionID
		}
		return rules[i].Position < rules[j].Position
	})
}

func (m *Manager) nextManualPositionLocked() int {
	position := 0
	for _, rule := range m.rules {
		if rule.Origin == OriginManual && rule.Position >= position {
			position = rule.Position + 1
		}
	}
	return position
}

func (m *Manager) normalizeManualPositionsLocked() {
	indices := make([]int, 0)
	for index, rule := range m.rules {
		if rule.Origin == OriginManual {
			indices = append(indices, index)
		}
	}
	sort.Slice(indices, func(i, j int) bool { return m.rules[indices[i]].Position < m.rules[indices[j]].Position })
	for position, index := range indices {
		m.rules[index].Position = position
	}
}

func (m *Manager) indexOfLocked(id string) int {
	for index := range m.rules {
		if m.rules[index].ID == id {
			return index
		}
	}
	return -1
}

func cloneRule(rule Rule) Rule {
	rule.Conditions = append([]Condition(nil), rule.Conditions...)
	for index := range rule.Conditions {
		rule.Conditions[index].Values = append([]string(nil), rule.Conditions[index].Values...)
	}
	return rule
}

func cloneRules(rules []Rule) []Rule {
	result := make([]Rule, len(rules))
	for index, rule := range rules {
		result[index] = cloneRule(rule)
	}
	return result
}

func subscriptionRuleID(subscriptionID, source string, occurrence int) string {
	sum := sha256.Sum256([]byte(subscriptionID + "\x00" + source + "\x00" + strconv.Itoa(occurrence)))
	return "sub-" + hex.EncodeToString(sum[:12])
}

func randomID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(value[:])
}

func (m *Manager) publish(eventType string, payload any) {
	if m.events != nil {
		_, _ = m.events.Publish(eventType, payload)
	}
}
