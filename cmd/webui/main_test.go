package main

import (
	"testing"

	"sing-box-webui/internal/api"
	"sing-box-webui/internal/application"
)

func TestDisabledWebAuthenticationConfiguresUnauthenticatedServer(t *testing.T) {
	t.Parallel()

	token, allowUnauthenticated := webServerAuthentication(application.Config{
		Address:        "127.0.0.1:11872",
		WebAuthEnabled: false,
	})
	if token != "" || !allowUnauthenticated {
		t.Fatalf("authentication mapping = token %q, allowUnauthenticated %v", token, allowUnauthenticated)
	}
	if _, err := api.NewServer(api.ServerConfig{
		Address:              "127.0.0.1:11872",
		WebToken:             token,
		AllowUnauthenticated: allowUnauthenticated,
	}); err != nil {
		t.Fatalf("NewServer() rejected disabled authentication config: %v", err)
	}
}

func TestChannelReservedPortsIncludeApplicationListeners(t *testing.T) {
	t.Parallel()
	ports := channelReservedPorts(application.Config{
		Address: "127.0.0.1:33334", DevOrigin: "http://127.0.0.1:33333", MixedPort: 2080,
	})
	want := []uint16{2080, 33334, 33333}
	if len(ports) != len(want) {
		t.Fatalf("reserved ports = %v, want %v", ports, want)
	}
	for index := range want {
		if ports[index] != want[index] {
			t.Fatalf("reserved ports = %v, want %v", ports, want)
		}
	}
}
