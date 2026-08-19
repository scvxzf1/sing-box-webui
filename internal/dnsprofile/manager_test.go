package dnsprofile

import (
	"os"
	"path/filepath"
	"testing"

	"sing-box-webui/internal/events"
)

func TestOpenManagerDefaultsToBuiltinProfile(t *testing.T) {
	t.Parallel()
	manager, err := OpenManager(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	profile := manager.Get()
	if len(profile.Servers) != 1 || profile.Servers[0].Tag != "dns-google" || profile.Servers[0].Type != "udp" || profile.Servers[0].Server != "8.8.8.8" {
		t.Fatalf("unexpected default servers: %+v", profile.Servers)
	}
	if profile.Final != "dns-google" || profile.Strategy != StrategyPreferIPv4 || profile.FakeIP.Enabled {
		t.Fatalf("unexpected default profile: %+v", profile)
	}
}

func TestUpdateValidatesAndPersists(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	manager, err := OpenManager(directory, nil)
	if err != nil {
		t.Fatal(err)
	}
	input := Profile{
		Servers: []Server{
			{Tag: "dns-local", Type: "udp", Server: "223.5.5.5"},
			{Tag: "dns-remote", Type: "https", Server: "dns.google"},
		},
		Final:    "dns-remote",
		Strategy: StrategyPreferIPv4,
		FakeIP:   FakeIP{Enabled: true},
	}
	updated, err := manager.Update(input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.FakeIP.Inet4Range != "198.18.0.0/15" || updated.FakeIP.Inet6Range != "fc00::/18" {
		t.Fatalf("fakeip ranges not defaulted: %+v", updated.FakeIP)
	}

	reopened, err := OpenManager(directory, nil)
	if err != nil {
		t.Fatal(err)
	}
	persisted := reopened.Get()
	if persisted.Final != "dns-remote" || persisted.Strategy != StrategyPreferIPv4 || !persisted.FakeIP.Enabled || len(persisted.Servers) != 2 {
		t.Fatalf("unexpected persisted profile: %+v", persisted)
	}
}

func TestUpdateRejectsInvalidProfiles(t *testing.T) {
	t.Parallel()
	valid := Profile{
		Servers:  []Server{{Tag: "dns-google", Type: "udp", Server: "8.8.8.8"}},
		Final:    "dns-google",
		Strategy: StrategyPreferIPv4,
	}
	cases := []struct {
		name   string
		mutate func(profile *Profile)
	}{
		{name: "no servers", mutate: func(p *Profile) { p.Servers = nil }},
		{name: "duplicate tag", mutate: func(p *Profile) {
			p.Servers = append(p.Servers, Server{Tag: "dns-google", Type: "udp", Server: "8.8.4.4"})
		}},
		{name: "bad tag", mutate: func(p *Profile) { p.Servers[0].Tag = "Bad Tag" }},
		{name: "unknown type", mutate: func(p *Profile) { p.Servers[0].Type = "doh3" }},
		{name: "missing address", mutate: func(p *Profile) { p.Servers[0].Server = "" }},
		{name: "address on local", mutate: func(p *Profile) {
			p.Servers = append(p.Servers, Server{Tag: "dns-local", Type: "local", Server: "223.5.5.5"})
			p.Final = "dns-google"
		}},
		{name: "numeric address", mutate: func(p *Profile) { p.Servers[0].Server = "12345" }},
		{name: "final not referenced", mutate: func(p *Profile) { p.Final = "dns-missing" }},
		{name: "bad strategy", mutate: func(p *Profile) { p.Strategy = "round_robin" }},
		{name: "fakeip with ipv6 strategy", mutate: func(p *Profile) {
			p.FakeIP = FakeIP{Enabled: true}
			p.Strategy = StrategyPreferIPv6
		}},
		{name: "fakeip bad range", mutate: func(p *Profile) {
			p.FakeIP = FakeIP{Enabled: true, Inet4Range: "198.18.0.0", Inet6Range: "fc00::/18"}
		}},
		{name: "port out of range", mutate: func(p *Profile) {
			port := 70000
			p.Servers[0].Port = &port
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			manager, err := OpenManager(t.TempDir(), nil)
			if err != nil {
				t.Fatal(err)
			}
			profile := valid
			profile.Servers = append([]Server(nil), valid.Servers...)
			testCase.mutate(&profile)
			if _, err := manager.Update(profile); err == nil {
				t.Fatalf("Update(%+v) succeeded, want validation error", profile)
			}
			current := manager.Get()
			if current.Final != valid.Final || len(current.Servers) != 1 {
				t.Fatalf("invalid update mutated stored profile: %+v", current)
			}
		})
	}
}

func TestUpdateDefaultsFinalForSingleServer(t *testing.T) {
	t.Parallel()
	manager, err := OpenManager(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := manager.Update(Profile{
		Servers:  []Server{{Tag: "dns-alibaba", Type: "udp", Server: "223.5.5.5"}},
		Strategy: StrategyIPv4Only,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Final != "dns-alibaba" {
		t.Fatalf("final = %q, want dns-alibaba", updated.Final)
	}
}

func TestUpdateRollbackOnPersistFailure(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	manager, err := OpenManager(directory, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager.path = filepath.Join(directory, "missing", "dns-profile.json")
	if _, err := manager.Update(Profile{
		Servers:  []Server{{Tag: "dns-alibaba", Type: "udp", Server: "223.5.5.5"}},
		Final:    "dns-alibaba",
		Strategy: StrategyPreferIPv4,
	}); err == nil {
		t.Fatal("Update succeeded with unwritable store")
	}
	current := manager.Get()
	if current.Final != "dns-google" {
		t.Fatalf("profile was not rolled back: %+v", current)
	}
}

func TestUpdatePublishesEvent(t *testing.T) {
	t.Parallel()
	broker := events.NewBroker(8, 1)
	manager, err := OpenManager(t.TempDir(), broker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Update(Profile{
		Servers:  []Server{{Tag: "dns-alibaba", Type: "udp", Server: "223.5.5.5"}},
		Strategy: StrategyPreferIPv4,
	}); err != nil {
		t.Fatal(err)
	}
	history := broker.History()
	if len(history) != 1 || history[0].Type != "dns-profile.updated" {
		t.Fatalf("published events = %+v, want one dns-profile.updated", history)
	}
}

func TestCorruptStoreFailsOpen(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "dns-profile.json"), []byte(`{"servers": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenManager(directory, nil); err == nil {
		t.Fatal("OpenManager succeeded with corrupt store")
	}
	if err := os.WriteFile(filepath.Join(directory, "dns-profile.json"), []byte(`{"servers": [], "final": "x", "strategy": "prefer_ipv4"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenManager(directory, nil); err == nil {
		t.Fatal("OpenManager succeeded with invalid stored profile")
	}
}
