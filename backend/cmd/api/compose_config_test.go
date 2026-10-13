package main

import (
	"net/netip"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The compose file is deployment configuration the backend trusts blindly:
// whatever TRUSTED_PROXY_CIDRS says is believed to be a reverse proxy whose
// X-Real-IP keys the rate limiters. These tests pin it to the compose network
// itself (CO-S1b-1) instead of every private range.

const composeFile = "../../docker-compose.yml"

var (
	composeTrustedDefault = regexp.MustCompile(`(?m)^\s*TRUSTED_PROXY_CIDRS:\s*\$\{TRUSTED_PROXY_CIDRS:-([^}]*)\}\s*$`)
	composeSubnet         = regexp.MustCompile(`(?m)^\s*-?\s*subnet:\s*(\S+)\s*$`)
)

func readCompose(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(composeFile)
	if err != nil {
		t.Fatalf("read compose file: %v", err)
	}
	return string(b)
}

func TestCompose_NetworkSubnetIsDeclaredExplicitly(t *testing.T) {
	m := composeSubnet.FindStringSubmatch(readCompose(t))
	if m == nil {
		t.Fatal("docker-compose.yml must declare the rrhh_go_net subnet (networks.rrhh_go_net.ipam.config) so the trusted proxy range is deterministic")
	}
	p, err := netip.ParsePrefix(m[1])
	if err != nil {
		t.Fatalf("subnet %q is not a CIDR: %v", m[1], err)
	}
	if p.Bits() < 16 {
		t.Fatalf("compose subnet %s is broader than a /16", p)
	}
}

func TestCompose_TrustedProxyDefaultIsOnlyTheComposeSubnet(t *testing.T) {
	src := readCompose(t)
	sub := composeSubnet.FindStringSubmatch(src)
	def := composeTrustedDefault.FindStringSubmatch(src)
	if sub == nil || def == nil {
		t.Fatalf("compose must declare a subnet and a TRUSTED_PROXY_CIDRS default (subnet=%v default=%v)", sub != nil, def != nil)
	}
	subnet := netip.MustParsePrefix(sub[1])

	cidrs := strings.Split(def[1], ",")
	if len(cidrs) != 1 {
		t.Fatalf("trusted proxies default = %q, want exactly the compose subnet", def[1])
	}
	got, err := netip.ParsePrefix(strings.TrimSpace(cidrs[0]))
	if err != nil {
		t.Fatalf("trusted default %q is not a CIDR: %v", def[1], err)
	}
	if got != subnet {
		t.Fatalf("trusted proxies default = %s, want the compose subnet %s", got, subnet)
	}
}

func TestEnvExample_TrustedProxiesDoNotListBroadPrivateRanges(t *testing.T) {
	b, err := os.ReadFile("../../env.example")
	if err != nil {
		t.Fatalf("read env.example: %v", err)
	}
	m := regexp.MustCompile(`(?m)^TRUSTED_PROXY_CIDRS=(.*)$`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("env.example must document TRUSTED_PROXY_CIDRS")
	}
	sub := composeSubnet.FindStringSubmatch(readCompose(t))
	if sub == nil {
		t.Fatal("docker-compose.yml must declare the compose subnet")
	}
	subnet := netip.MustParsePrefix(sub[1])
	for _, c := range strings.Split(m[1], ",") {
		p, err := netip.ParsePrefix(strings.TrimSpace(c))
		if err != nil {
			t.Fatalf("env.example CIDR %q: %v", c, err)
		}
		if p.Bits() < 16 {
			t.Fatalf("env.example trusts the broad range %s", p)
		}
		if p != subnet {
			t.Fatalf("env.example trusts %s, want the compose subnet %s", p, subnet)
		}
	}
}
