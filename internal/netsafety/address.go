package netsafety

import "net/netip"

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fec0::/10"),
}

var nat64WellKnownPrefix = netip.MustParsePrefix("64:ff9b::/96")

// AllowedPublicAddress limits outbound probes and fetches to public addresses.
func AllowedPublicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() ||
		address.IsLoopback() ||
		address.IsPrivate() ||
		address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() ||
		address.IsMulticast() ||
		address.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	if nat64WellKnownPrefix.Contains(address) {
		bytes := address.As16()
		embedded := netip.AddrFrom4([4]byte{bytes[12], bytes[13], bytes[14], bytes[15]})
		return AllowedPublicAddress(embedded)
	}
	return true
}
