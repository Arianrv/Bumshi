// Package ssrfguard prevents server-side request forgery (SSRF) by refusing to
// connect to any address that is not a globally-routable public IP. It is meant
// to be installed as a net.Dialer.Control hook, which runs after DNS resolution
// but before the connection is made — so it also defeats DNS-rebinding, because
// it inspects the concrete IP being dialed, not the hostname.
package ssrfguard

import (
	"errors"
	"net"
	"net/netip"
	"syscall"
)

// ErrBlockedAddress is returned when a destination is not a permitted public IP.
var ErrBlockedAddress = errors.New("ssrfguard: destination address is not permitted")

// blockedPrefixes are additional ranges not already covered by the netip
// predicates in IsPublic (loopback, private, link-local, multicast, etc.).
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),   // RFC 6598 CGNAT
	netip.MustParsePrefix("192.0.0.0/24"),    // RFC 6890 IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // RFC 2544 benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved
	netip.MustParsePrefix("2001:db8::/32"),   // IPv6 documentation
}

// Control is a net.Dialer.Control hook. It rejects any connection whose resolved
// address is not a public IP.
func Control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return ErrBlockedAddress
	}
	if !IsPublic(addr) {
		return ErrBlockedAddress
	}
	return nil
}

// IsPublic reports whether addr is a globally-routable public address that the
// proxy is permitted to reach.
func IsPublic(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() {
		return false
	}
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsUnspecified() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() || addr.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}
