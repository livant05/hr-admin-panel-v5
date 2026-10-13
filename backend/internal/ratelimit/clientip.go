package ratelimit

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP returns the address a request should be rate limited under.
//
// The peer address (r.RemoteAddr) is the only value the client cannot forge.
// X-Real-IP is honoured solely when the peer lies inside a trusted proxy
// prefix, because a trusted reverse proxy overwrites that header with the
// address it saw. X-Forwarded-For is deliberately never read: clients can
// append to it freely. An empty trusted list trusts nobody.
func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	peer, ok := peerAddr(r.RemoteAddr)
	if !ok {
		return r.RemoteAddr
	}
	if isTrusted(peer, trusted) {
		if real, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
			return real.Unmap().String()
		}
	}
	return peer.String()
}

// peerAddr parses "ip:port" or a bare IP, normalising IPv4-mapped IPv6.
func peerAddr(remoteAddr string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(remoteAddr); err == nil {
		return ap.Addr().Unmap(), true
	}
	if a, err := netip.ParseAddr(remoteAddr); err == nil {
		return a.Unmap(), true
	}
	return netip.Addr{}, false
}

func isTrusted(ip netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// ParseCIDRs parses a comma-separated list of CIDR prefixes (the
// TRUSTED_PROXY_CIDRS format). Blank entries are skipped; an empty list yields
// no prefixes. A bare address without a prefix length is rejected so an
// operator cannot trust a whole network by accident or a single host
// ambiguously.
func ParseCIDRs(csv string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(csv, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", part, err)
		}
		out = append(out, p)
	}
	return out, nil
}
