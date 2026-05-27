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

func TestParseAdminMachineRefDefaultsProject(t *testing.T) {
	ref, err := ParseAdminMachineRef("acme/codex")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Tenant != "acme" || ref.Project != DefaultProjectName || ref.Machine != "codex" {
		t.Fatalf("ref = %#v", ref)
	}
}

func TestParseAdminMachineRefWithProject(t *testing.T) {
	ref, err := ParseAdminMachineRef("acme/website/codex")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Tenant != "acme" || ref.Project != "website" || ref.Machine != "codex" {
		t.Fatalf("ref = %#v", ref)
	}
}

func TestParseUserMachineRefDefaultsProject(t *testing.T) {
	projectRef, machine, err := ParseUserMachineRef("codex", "")
	if err != nil {
		t.Fatal(err)
	}
	if projectRef.Project != DefaultProjectName || machine != "codex" {
		t.Fatalf("projectRef = %#v, machine = %q", projectRef, machine)
	}
}

func TestParseUserMachineRefUsesCurrentProject(t *testing.T) {
	projectRef, machine, err := ParseUserMachineRef("codex", "website")
	if err != nil {
		t.Fatal(err)
	}
	if projectRef.Project != "website" || machine != "codex" {
		t.Fatalf("projectRef = %#v, machine = %q", projectRef, machine)
	}
}

func TestParseUserMachineRefPreservesExplicitProject(t *testing.T) {
	projectRef, machine, err := ParseUserMachineRef("default/codex", "website")
	if err != nil {
		t.Fatal(err)
	}
	if projectRef.Project != "default" || machine != "codex" {
		t.Fatalf("projectRef = %#v, machine = %q", projectRef, machine)
	}
}

func TestParseUserMachineRefAcceptsColonProject(t *testing.T) {
	projectRef, machine, err := ParseUserMachineRef("default:codex", "website")
	if err != nil {
		t.Fatal(err)
	}
	if projectRef.Project != "default" || machine != "codex" {
		t.Fatalf("projectRef = %#v, machine = %q", projectRef, machine)
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

func TestTenantInfraAndNativeIncusProjectNames(t *testing.T) {
	main, err := TenantIncusProjectName(TenantRef{Tenant: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if got := TenantInfraIncusProjectName(main); got != "sc-acme-infra" {
		t.Fatalf("infra name = %q, want sc-acme-infra", got)
	}
	if got := TenantNativeIncusProjectName(main); got != "sc-acme-native" {
		t.Fatalf("native name = %q, want sc-acme-native", got)
	}
}

func TestTenantIncusProjectNameLengthLimit(t *testing.T) {
	// Max tenant name that still leaves room for "-native" suffix (total ≤ 63).
	// "sc-" (3) + tenant (53) = 56; + "-native" (7) = 63. OK.
	longTenant := strings.Repeat("a", 53)
	name, err := TenantIncusProjectName(TenantRef{Tenant: longTenant})
	if err != nil {
		t.Fatalf("unexpected error for 53-char tenant: %v", err)
	}
	if len(TenantNativeIncusProjectName(name)) > 63 {
		t.Fatalf("native project name %q exceeds 63 chars", TenantNativeIncusProjectName(name))
	}
	// One character too long.
	tooLong := strings.Repeat("a", 54)
	if _, err := TenantIncusProjectName(TenantRef{Tenant: tooLong}); err == nil {
		t.Fatal("expected error for 54-char tenant")
	}
}

func TestMachineIncusInstanceName(t *testing.T) {
	name, err := MachineIncusInstanceName(MachineRef{Tenant: "acme", Project: "website", Machine: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "website-codex" {
		t.Fatalf("name = %q, want website-codex", name)
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
