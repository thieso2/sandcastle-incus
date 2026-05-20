package cidr

import "testing"

func TestAllocateFirstProjectCIDR(t *testing.T) {
	prefix, err := Allocate("10.248.0.0/16", 24, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := prefix.String(); got != "10.248.0.0/24" {
		t.Fatalf("prefix = %q, want 10.248.0.0/24", got)
	}
}

func TestAllocateSkipsOccupiedCIDRs(t *testing.T) {
	prefix, err := Allocate("10.248.0.0/16", 24, []string{
		"10.248.0.0/24",
		"10.248.1.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := prefix.String(); got != "10.248.2.0/24" {
		t.Fatalf("prefix = %q, want 10.248.2.0/24", got)
	}
}

func TestAllocateAvoidsOverlappingLiveNetwork(t *testing.T) {
	prefix, err := Allocate("10.248.0.0/16", 24, []string{
		"10.248.0.0/23",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := prefix.String(); got != "10.248.2.0/24" {
		t.Fatalf("prefix = %q, want 10.248.2.0/24", got)
	}
}

func TestAllocateReportsExhaustion(t *testing.T) {
	_, err := Allocate("10.248.0.0/30", 30, []string{"10.248.0.0/30"})
	if err == nil {
		t.Fatal("expected exhaustion error")
	}
}

func TestAllocateRejectsIPv6(t *testing.T) {
	_, err := Allocate("fd7a:115c:a1e0::/48", 64, nil)
	if err == nil {
		t.Fatal("expected IPv6 error")
	}
}

func TestRoleAddress(t *testing.T) {
	prefix, err := Allocate("10.248.0.0/16", 24, nil)
	if err != nil {
		t.Fatal(err)
	}
	addr, err := RoleAddress(prefix, 53)
	if err != nil {
		t.Fatal(err)
	}
	if got := addr.String(); got != "10.248.0.53" {
		t.Fatalf("addr = %q, want 10.248.0.53", got)
	}
}
