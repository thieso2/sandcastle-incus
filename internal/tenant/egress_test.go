package tenant

import (
	"strings"
	"testing"
)

// With egress off the bridge carries only the CoreDNS resolver option — the
// pre-ADR-0026 value, byte-identical so existing bridges stay converged.
func TestBridgeDHCPOptionsOff(t *testing.T) {
	got := BridgeDHCPOptions("10.249.7.3", "10.249.7.1", false)
	if got != "dhcp-option=6,10.249.7.3" {
		t.Fatalf("BridgeDHCPOptions(off) = %q", got)
	}
}

// With egress on, option 121 steers the CGNAT range at the sidecar AND must
// carry the default route: RFC 3442 makes clients that receive option 121
// ignore the router option, so omitting 0.0.0.0/0 would cut every machine off
// from its gateway.
func TestBridgeDHCPOptionsOnCarriesDefaultRoute(t *testing.T) {
	got := BridgeDHCPOptions("10.249.7.3", "10.249.7.1", true)
	want := "dhcp-option=6,10.249.7.3\ndhcp-option=121,100.64.0.0/10,10.249.7.3,0.0.0.0/0,10.249.7.1"
	if got != want {
		t.Fatalf("BridgeDHCPOptions(on) = %q, want %q", got, want)
	}
}

// The ruleset must be an atomic idempotent replace (declare/delete/declare) and
// scope the masquerade to tenant-sourced, CGNAT-destined traffic on tailscale0.
func TestSidecarEgressNftRuleset(t *testing.T) {
	got := SidecarEgressNftRuleset("10.249.7.0/24")
	if !strings.HasPrefix(got, "table ip sandcastle-egress\ndelete table ip sandcastle-egress\n") {
		t.Fatalf("ruleset must start with the declare/delete idiom, got:\n%s", got)
	}
	for _, want := range []string{
		`oifname "tailscale0"`,
		"ip saddr 10.249.7.0/24",
		"ip daddr 100.64.0.0/10",
		"masquerade",
		"type nat hook postrouting",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("ruleset missing %q:\n%s", want, got)
		}
	}
}
