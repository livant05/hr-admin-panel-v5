package ratelimit

import "net/netip"

// Key maps a client address (as returned by ClientIP) to the bucket key the
// limiter should charge. IPv4 and IPv4-mapped IPv6 addresses are keyed per
// address. Native IPv6 addresses are keyed by their /64 prefix: a single
// subscriber is normally delegated a whole /64 (or more), so keying on the
// full /128 would let one client rotate through 2^64 addresses for fresh
// budgets. Input that is not an IP address is returned unchanged.
func Key(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is4() {
		return addr.String()
	}
	return netip.PrefixFrom(addr, 64).Masked().String()
}
