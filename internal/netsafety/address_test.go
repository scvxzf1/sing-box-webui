package netsafety

import (
	"net/netip"
	"testing"
)

func TestAllowedPublicAddress(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"127.0.0.1", "::1", "0.1.2.3", "10.0.0.1", "192.168.1.1", "169.254.1.1", "224.0.0.1", "::ffff:127.0.0.1", "100.64.0.1", "198.18.0.144", "192.0.2.1", "64:ff9b::7f00:1", "64:ff9b::a00:1", "64:ff9b:1::1", "2001:db8::1", "fec0::1"} {
		if AllowedPublicAddress(netip.MustParseAddr(raw)) {
			t.Fatalf("AllowedPublicAddress(%q) = true", raw)
		}
	}
	if !AllowedPublicAddress(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("public address was blocked")
	}
}
