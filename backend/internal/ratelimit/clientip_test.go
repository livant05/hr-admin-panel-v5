package ratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/livant05/rrhh-go/internal/ratelimit"
)

func mustCIDRs(t *testing.T, csv string) []netip.Prefix {
	t.Helper()
	p, err := ratelimit.ParseCIDRs(csv)
	if err != nil {
		t.Fatalf("ParseCIDRs(%q): %v", csv, err)
	}
	return p
}

func TestClientIP(t *testing.T) {
	proxies := "10.0.0.0/8,172.16.0.0/12"
	tests := []struct {
		name       string
		trusted    string
		remoteAddr string
		realIP     string
		xff        string
		want       string
	}{
		{"trusted peer with valid X-Real-IP uses X-Real-IP", proxies, "172.18.0.5:44000", "203.0.113.9", "", "203.0.113.9"},
		{"untrusted peer with forged X-Real-IP uses peer", proxies, "198.51.100.7:5000", "203.0.113.9", "", "198.51.100.7"},
		{"X-Forwarded-For is never read, even from a trusted peer", proxies, "172.18.0.5:44000", "", "203.0.113.9", "172.18.0.5"},
		{"X-Forwarded-For ignored when X-Real-IP is present too", proxies, "172.18.0.5:44000", "203.0.113.9", "1.1.1.1, 2.2.2.2", "203.0.113.9"},
		{"X-Forwarded-For from an untrusted peer is ignored", proxies, "198.51.100.7:5000", "", "1.1.1.1", "198.51.100.7"},
		{"invalid X-Real-IP falls back to peer", proxies, "172.18.0.5:44000", "not-an-ip", "", "172.18.0.5"},
		{"empty X-Real-IP falls back to peer", proxies, "172.18.0.5:44000", "", "", "172.18.0.5"},
		{"empty trusted list trusts nobody", "", "172.18.0.5:44000", "203.0.113.9", "", "172.18.0.5"},
		{"trusted IPv6 peer with IPv6 X-Real-IP", "fd00::/8", "[fd00::5]:44000", "2001:db8::1", "", "2001:db8::1"},
		{"IPv4-mapped IPv6 peer matches an IPv4 prefix", proxies, "[::ffff:10.1.2.3]:44000", "203.0.113.9", "", "203.0.113.9"},
		{"X-Real-IP with surrounding spaces is accepted", proxies, "10.1.2.3:44000", " 203.0.113.9 ", "", "203.0.113.9"},
		{"peer without port is still parsed", proxies, "198.51.100.7", "", "", "198.51.100.7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.realIP != "" {
				r.Header.Set("X-Real-IP", tt.realIP)
			}
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			if got := ratelimit.ClientIP(r, mustCIDRs(t, tt.trusted)); got != tt.want {
				t.Fatalf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClientIP_UnparseablePeerIsReturnedVerbatim(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "pipe"
	r.Header.Set("X-Real-IP", "203.0.113.9")
	if got := ratelimit.ClientIP(r, mustCIDRs(t, "10.0.0.0/8")); got != "pipe" {
		t.Fatalf("an unparseable peer cannot be trusted; got %q, want the raw peer", got)
	}
}

func TestParseCIDRs(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr string
	}{
		{"empty means trust nobody", "", nil, ""},
		{"blank entries are skipped", " , ,", nil, ""},
		{"single prefix", "10.0.0.0/8", []string{"10.0.0.0/8"}, ""},
		{"list with spaces", " 10.0.0.0/8 , 172.16.0.0/12,192.168.0.0/16 ", []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}, ""},
		{"ipv6 prefix", "fd00::/8", []string{"fd00::/8"}, ""},
		{"invalid entry names itself", "10.0.0.0/8,bogus", nil, "bogus"},
		{"bare address without length is rejected", "10.0.0.1", nil, "10.0.0.1"},
		{"bad prefix length", "10.0.0.0/99", nil, "10.0.0.0/99"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ratelimit.ParseCIDRs(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected an error naming %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i, p := range got {
				if p.String() != tt.want[i] {
					t.Fatalf("prefix %d = %s, want %s", i, p, tt.want[i])
				}
			}
		})
	}
}
