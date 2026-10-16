package ratelimit

import "testing"

func TestKey(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want string
	}{
		{"ipv4 stays per address", "203.0.113.9", "203.0.113.9"},
		{"ipv4-mapped ipv6 maps to the ipv4 key", "::ffff:203.0.113.9", "203.0.113.9"},
		{"ipv6 keyed by its /64", "2001:db8:1:2:aaaa:bbbb:cccc:dddd", "2001:db8:1:2::/64"},
		{"ipv6 sibling in the same /64 shares the key", "2001:db8:1:2:1::1", "2001:db8:1:2::/64"},
		{"ipv6 in another /64 gets another key", "2001:db8:1:3::1", "2001:db8:1:3::/64"},
		{"ipv6 zone is dropped", "fe80::1%eth0", "fe80::/64"},
		{"unparsable input is used verbatim", "not-an-ip", "not-an-ip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Key(tt.ip); got != tt.want {
				t.Fatalf("Key(%q) = %q, want %q", tt.ip, got, tt.want)
			}
		})
	}
}
