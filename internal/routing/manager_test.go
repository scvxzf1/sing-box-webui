package routing

import (
	"path/filepath"
	"testing"

	"sing-box-webui/internal/subscription"
)

func TestManagerPersistsAndCompilesManualRules(t *testing.T) {
	manager, err := OpenManager(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := manager.Create(CreateInput{
		Name: "内网直连", Enabled: true, Action: ActionDirect,
		Conditions: []Condition{{Type: "ip_is_private"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := manager.Compiled()
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled) != 1 || compiled[0]["outbound"] != "direct" || compiled[0]["ip_is_private"] != true {
		t.Fatalf("compiled = %#v", compiled)
	}
	if got := manager.List(); len(got) != 2 || got[0].ID != rule.ID || got[1].ID != BuiltinGlobalID {
		t.Fatalf("list = %#v", got)
	}
}

func TestRuleMutationsRollBackWhenPersistenceFails(t *testing.T) {
	directory := t.TempDir()
	manager, err := OpenManager(directory, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Create(CreateInput{
		Name: "first", Enabled: true, Action: ActionDirect,
		Conditions: []Condition{{Type: "domain", Values: []string{"first.example"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create(CreateInput{
		Name: "second", Enabled: true, Action: ActionProxy,
		Conditions: []Condition{{Type: "domain", Values: []string{"second.example"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.path = filepath.Join(directory, "missing", "rules.json")
	name := "changed"
	if _, err := manager.Update(first.ID, UpdateInput{Name: &name}); err == nil {
		t.Fatal("Update() succeeded with an unavailable store")
	}
	if got := manager.List()[0]; got.Name != "first" {
		t.Fatalf("Update() changed memory after persistence failure: %+v", got)
	}
	if _, err := manager.Reorder([]string{second.ID, first.ID}); err == nil {
		t.Fatal("Reorder() succeeded with an unavailable store")
	}
	listed := manager.List()
	if listed[0].ID != first.ID || listed[1].ID != second.ID {
		t.Fatalf("Reorder() changed memory after persistence failure: %+v", listed)
	}
}

func TestSubscriptionRulesDefaultOffAndPreserveEnabledState(t *testing.T) {
	manager, err := OpenManager(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	imported := []subscription.ImportedRule{{
		Name: "example", Action: "proxy", Supported: true, Source: `{"domain_suffix":["example.com"]}`,
		Conditions: []subscription.ImportedRuleCondition{{Type: "domain_suffix", Values: []string{"example.com"}}},
	}}
	if err := manager.SyncSubscriptionRules("sub-1", "订阅一", imported); err != nil {
		t.Fatal(err)
	}
	rule := manager.List()[0]
	if rule.Enabled {
		t.Fatal("imported rule must be disabled initially")
	}
	enabled := true
	if _, err := manager.Update(rule.ID, UpdateInput{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SyncSubscriptionRules("sub-1", "订阅一", imported); err != nil {
		t.Fatal(err)
	}
	if !manager.List()[0].Enabled {
		t.Fatal("refresh did not preserve enabled state")
	}
}

func TestSubscriptionSyncReloadsRuntimeAndKeepsDuplicateIDsUnique(t *testing.T) {
	manager, err := OpenManager(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	reloads := 0
	manager.SetReload(func() error { reloads++; return nil })
	source := subscription.ImportedRule{
		Name: "duplicate", Action: "direct", Supported: true, Source: `{"ip_is_private":true,"outbound":"direct"}`,
		Conditions: []subscription.ImportedRuleCondition{{Type: "ip_is_private"}},
	}
	if err := manager.SyncSubscriptionRules("sub-1", "订阅一", []subscription.ImportedRule{source, source}); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReloadRules(); err != nil {
		t.Fatal(err)
	}
	rules := manager.List()
	if reloads != 1 {
		t.Fatalf("reloads = %d, want 1", reloads)
	}
	if len(rules) != 3 || rules[0].ID == rules[1].ID {
		t.Fatalf("duplicate rule IDs were not unique: %#v", rules)
	}
}

func TestManagerRejectsEnablingUnsupportedSubscriptionRule(t *testing.T) {
	manager, err := OpenManager(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SyncSubscriptionRules("sub-1", "订阅一", []subscription.ImportedRule{{
		Name: "rule-set", Action: "proxy", Supported: false, UnsupportedReason: "unsupported condition", Source: `{}`,
	}}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := manager.Update(manager.List()[0].ID, UpdateInput{Enabled: &enabled}); err == nil {
		t.Fatal("Update() accepted unsupported rule")
	}
}

func TestRulePoolsPersistAndCompileBetweenManualAndSubscriptionRules(t *testing.T) {
	directory := t.TempDir()
	manager, err := OpenManager(directory, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(CreateInput{
		Name: "manual", Enabled: true, Action: ActionDirect,
		Conditions: []Condition{{Type: "domain", Values: []string{"manual.example"}}},
	}); err != nil {
		t.Fatal(err)
	}
	pool, err := manager.CreatePool(CreatePoolInput{
		Name: "blocked services", Enabled: true,
		Rules: []CreateInput{
			{Name: "blocked", Enabled: true, Action: ActionBlock, Conditions: []Condition{{Type: "domain_suffix", Values: []string{"blocked.example"}}}},
			{Name: "disabled", Enabled: false, Action: ActionProxy, Conditions: []Condition{{Type: "domain", Values: []string{"disabled.example"}}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	imported := []subscription.ImportedRule{{
		Name: "subscription", Action: "proxy", Supported: true, Source: `{"domain":["subscription.example"]}`,
		Conditions: []subscription.ImportedRuleCondition{{Type: "domain", Values: []string{"subscription.example"}}},
	}}
	if err := manager.SyncSubscriptionRules("sub-1", "subscription", imported); err != nil {
		t.Fatal(err)
	}
	subscriptionRule := manager.List()[1]
	enabled := true
	if _, err := manager.Update(subscriptionRule.ID, UpdateInput{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	compiled, err := manager.Compiled()
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled) != 3 || compiled[0]["outbound"] != "direct" || compiled[1]["outbound"] != "block" || compiled[2]["outbound"] != "proxy" {
		t.Fatalf("compiled order = %#v", compiled)
	}

	reopened, err := OpenManager(directory, nil)
	if err != nil {
		t.Fatal(err)
	}
	pools := reopened.ListPools()
	if len(pools) != 1 || pools[0].ID != pool.ID || len(pools[0].Rules) != 2 {
		t.Fatalf("persisted pools = %#v", pools)
	}
}

func TestRulePoolUpdateValidatesWholeReplacementBeforePersisting(t *testing.T) {
	manager, err := OpenManager(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := manager.CreatePool(CreatePoolInput{
		Name: "pool", Enabled: true,
		Rules: []CreateInput{{Name: "valid", Enabled: true, Action: ActionDirect, Conditions: []Condition{{Type: "ip_is_private"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	invalid := []CreateInput{{Name: "invalid", Enabled: true, Action: ActionDirect}}
	if _, err := manager.UpdatePool(pool.ID, UpdatePoolInput{Rules: &invalid}); err == nil {
		t.Fatal("UpdatePool() accepted invalid replacement")
	}
	stored := manager.ListPools()[0]
	if len(stored.Rules) != 1 || stored.Rules[0].Name != "valid" {
		t.Fatalf("pool changed after rejected update: %#v", stored)
	}
	empty := []CreateInput{}
	if _, err := manager.UpdatePool(pool.ID, UpdatePoolInput{Rules: &empty}); err != nil {
		t.Fatal(err)
	}
	if len(manager.ListPools()[0].Rules) != 0 {
		t.Fatal("pool was not cleared")
	}
}
