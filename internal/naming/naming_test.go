package naming

import (
	"strings"
	"testing"
)

func TestParseTenantRef(t *testing.T) {
	ref, err := ParseTenantRef("acme")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Tenant != "acme" {
		t.Fatalf("ref = %#v", ref)
	}
}

func TestParseProjectRef(t *testing.T) {
	ref, err := ParseProjectRef("acme/website")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Tenant != "acme" || ref.Project != "website" {
		t.Fatalf("ref = %#v", ref)
	}
}

func TestTenantIncusProjectName(t *testing.T) {
	name, err := TenantIncusProjectName(TenantRef{Tenant: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "sc-acme" {
		t.Fatalf("name = %q, want sc-acme", name)
	}
}

func TestValidateIncusProjectPrefix(t *testing.T) {
	if err := ValidateIncusProjectPrefix("dev"); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"", "s", "Bad", "bad_prefix", "bad.prefix"} {
		t.Run(prefix, func(t *testing.T) {
			if err := ValidateIncusProjectPrefix(prefix); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestValidateIncusProjectName(t *testing.T) {
	for _, name := range []string{"sc-infra", "sc-acme"} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateIncusProjectName(name); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, name := range []string{"", "s", "Bad", "bad_project", "bad.project"} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateIncusProjectName(name); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestValidateProjectNameAcceptsLeadingDigit(t *testing.T) {
	if err := ValidateProjectName("7ed"); err != nil {
		t.Fatal(err)
	}
}

func TestReservedProjectNames(t *testing.T) {
	for _, name := range []string{"default", "admin", "ca", "dns", "infra", "route", "tailscale"} {
		if !IsReservedProjectName(name) {
			t.Fatalf("%q should be reserved", name)
		}
	}
	if IsReservedProjectName("website") {
		t.Fatal("website should not be reserved")
	}
}

func TestReservedMachineNames(t *testing.T) {
	for _, name := range []string{"admin", "ca", "dns", "infra", "route", "tailscale", "sc-ca", "sc-dns"} {
		if !IsReservedInfrastructureName(name) {
			t.Fatalf("%q should be reserved", name)
		}
	}
	if IsReservedInfrastructureName("codex") {
		t.Fatal("codex should not be reserved")
	}
}

func TestValidateMachineName(t *testing.T) {
	if err := ValidateMachineName("codex"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bad_name", "x", "sc-dns"} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateMachineName(name); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

// Incus caps project names at 63 characters. v2 appends `-<project>` to the
// tenant's infra project name, so a tenant name short enough for v1 (which only
// had to leave room for the 7-char `-native` suffix) can still be too long for
// v2's `-default`. What must hold is that V2ProjectName fails closed rather than
// handing Incus a name it will reject.
func TestV2ProjectNameLengthLimit(t *testing.T) {
	for _, size := range []int{53, 54, 60} {
		tenant := strings.Repeat("a", size)
		project, err := V2ProjectName("sc", tenant, DefaultProjectName)
		if err != nil {
			continue // fails closed: acceptable
		}
		if len(project) > 63 {
			t.Fatalf("V2ProjectName(%d-char tenant) = %q (%d chars), over the 63-char Incus limit", size, project, len(project))
		}
	}
	// A comfortably short tenant still round-trips.
	project, err := V2ProjectName("sc", "acme", DefaultProjectName)
	if err != nil {
		t.Fatal(err)
	}
	if project != "sc-acme-default" {
		t.Fatalf("V2ProjectName = %q", project)
	}
	if _, err := TenantIncusProjectName(TenantRef{Tenant: strings.Repeat("a", 54)}); err == nil {
		t.Fatal("expected error for 54-char tenant")
	}
}
