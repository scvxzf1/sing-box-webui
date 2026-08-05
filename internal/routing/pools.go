package routing

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxPoolRules = 512

func (m *Manager) ListPools() []RulePool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := cloneRulePools(m.pools)
	sortRulePools(result)
	return result
}

func (m *Manager) CreatePool(input CreatePoolInput) (RulePool, error) {
	name, rules, err := validatePoolInput(input.Name, input.Rules)
	if err != nil {
		return RulePool{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	pool := RulePool{
		ID: randomID(), Name: name, Enabled: input.Enabled,
		Position: m.nextPoolPositionLocked(), Rules: rules,
	}
	m.pools = append(m.pools, pool)
	if err := m.persistPoolsLocked(); err != nil {
		m.pools = m.pools[:len(m.pools)-1]
		return RulePool{}, err
	}
	m.publish("rule-pool.created", map[string]string{"poolId": pool.ID})
	return cloneRulePool(pool), nil
}

func (m *Manager) UpdatePool(id string, input UpdatePoolInput) (RulePool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.poolIndexLocked(id)
	if index < 0 {
		return RulePool{}, ErrPoolNotFound
	}
	previous := cloneRulePool(m.pools[index])
	current := cloneRulePool(previous)
	if input.Name != nil {
		name, err := validatePoolName(*input.Name)
		if err != nil {
			return RulePool{}, err
		}
		current.Name = name
	}
	if input.Enabled != nil {
		current.Enabled = *input.Enabled
	}
	if input.Rules != nil {
		_, rules, err := validatePoolInput(current.Name, *input.Rules)
		if err != nil {
			return RulePool{}, err
		}
		current.Rules = rules
	}
	m.pools[index] = current
	if err := m.persistPoolsLocked(); err != nil {
		m.pools[index] = previous
		return RulePool{}, err
	}
	m.publish("rule-pool.updated", map[string]string{"poolId": id})
	return cloneRulePool(current), nil
}

func (m *Manager) DeletePool(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.poolIndexLocked(id)
	if index < 0 {
		return ErrPoolNotFound
	}
	previous := cloneRulePools(m.pools)
	m.pools = append(m.pools[:index], m.pools[index+1:]...)
	m.normalizePoolPositionsLocked()
	if err := m.persistPoolsLocked(); err != nil {
		m.pools = previous
		return err
	}
	m.publish("rule-pool.deleted", map[string]string{"poolId": id})
	return nil
}

func (m *Manager) ReorderPools(ids []string) ([]RulePool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneRulePools(m.pools)
	if len(ids) != len(m.pools) {
		return nil, fmt.Errorf("order must include every rule pool exactly once")
	}
	indices := make(map[string]int, len(m.pools))
	for index, pool := range m.pools {
		indices[pool.ID] = index
	}
	seen := make(map[string]struct{}, len(ids))
	for position, id := range ids {
		index, exists := indices[id]
		if !exists {
			return nil, fmt.Errorf("order contains an unknown rule pool")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("order contains a duplicate rule pool")
		}
		seen[id] = struct{}{}
		m.pools[index].Position = position
	}
	if err := m.persistPoolsLocked(); err != nil {
		m.pools = previous
		return nil, err
	}
	m.publish("rule-pools.reordered", map[string]int{"count": len(ids)})
	result := cloneRulePools(m.pools)
	sortRulePools(result)
	return result, nil
}

func validatePoolInput(name string, inputs []CreateInput) (string, []PoolRule, error) {
	name, err := validatePoolName(name)
	if err != nil {
		return "", nil, err
	}
	if len(inputs) > maxPoolRules {
		return "", nil, fmt.Errorf("rule pool cannot contain more than %d rules", maxPoolRules)
	}
	rules := make([]PoolRule, 0, len(inputs))
	for position, input := range inputs {
		ruleName, conditions, action, err := validateRuleInput(input.Name, input.Conditions, input.Action)
		if err != nil {
			return "", nil, fmt.Errorf("rule %d: %w", position+1, err)
		}
		rules = append(rules, PoolRule{
			ID: randomID(), Name: ruleName, Enabled: input.Enabled,
			Conditions: conditions, Action: action, Position: position,
		})
	}
	return name, rules, nil
}

func validatePoolName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 {
		return "", fmt.Errorf("rule pool name must contain 1-80 characters")
	}
	return name, nil
}

func (m *Manager) persistPoolsLocked() error {
	content, err := json.MarshalIndent(m.pools, "", "  ")
	if err != nil {
		return fmt.Errorf("encode rule pools: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(m.poolPath), ".rule-pools-*.tmp")
	if err != nil {
		return fmt.Errorf("create rule pool store: %w", err)
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
	if err := os.Rename(name, m.poolPath); err != nil {
		return fmt.Errorf("commit rule pools: %w", err)
	}
	committed = true
	return nil
}

func (m *Manager) poolIndexLocked(id string) int {
	for index := range m.pools {
		if m.pools[index].ID == id {
			return index
		}
	}
	return -1
}

func (m *Manager) nextPoolPositionLocked() int {
	position := 0
	for _, pool := range m.pools {
		if pool.Position >= position {
			position = pool.Position + 1
		}
	}
	return position
}

func (m *Manager) normalizePoolPositionsLocked() {
	indices := make([]int, len(m.pools))
	for index := range m.pools {
		indices[index] = index
	}
	sort.Slice(indices, func(i, j int) bool { return m.pools[indices[i]].Position < m.pools[indices[j]].Position })
	for position, index := range indices {
		m.pools[index].Position = position
	}
}

func sortRulePools(pools []RulePool) {
	sort.SliceStable(pools, func(i, j int) bool { return pools[i].Position < pools[j].Position })
	for index := range pools {
		sort.SliceStable(pools[index].Rules, func(i, j int) bool {
			return pools[index].Rules[i].Position < pools[index].Rules[j].Position
		})
	}
}

func cloneRulePool(pool RulePool) RulePool {
	rules := make([]PoolRule, len(pool.Rules))
	copy(rules, pool.Rules)
	pool.Rules = rules
	for index := range pool.Rules {
		pool.Rules[index].Conditions = append([]Condition(nil), pool.Rules[index].Conditions...)
		for conditionIndex := range pool.Rules[index].Conditions {
			pool.Rules[index].Conditions[conditionIndex].Values = append([]string(nil), pool.Rules[index].Conditions[conditionIndex].Values...)
		}
	}
	return pool
}

func cloneRulePools(pools []RulePool) []RulePool {
	result := make([]RulePool, len(pools))
	for index, pool := range pools {
		result[index] = cloneRulePool(pool)
	}
	return result
}
