package latency

import (
	"context"
	"net/http"
	"net/netip"

	"sing-box-webui/internal/netresolve"
)

func resolveNodeAddresses(ctx context.Context, host string) ([]netip.Addr, error) {
	return netresolve.PublicAddresses(ctx, host)
}

func resolveDoH(ctx context.Context, client *http.Client, endpoint, host string) ([]netip.Addr, error) {
	return netresolve.ResolveDoH(ctx, client, endpoint, host)
}
