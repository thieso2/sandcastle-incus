package naming

import "testing"

func TestV2TenantInfraProjectName(t *testing.T) {
	name, err := V2TenantInfraProjectName(V2IncusProjectPrefix, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if name != "sc2-acme" {
		t.Fatalf("name = %q, want sc2-acme", name)
	}
}

func TestV2TenantInfraProjectNameRejectsBadTenant(t *testing.T) {
	if _, err := V2TenantInfraProjectName(V2IncusProjectPrefix, "Bad Name"); err == nil {
		t.Fatal("expected error for invalid tenant")
	}
}

func TestV2ProjectName(t *testing.T) {
	name, err := V2ProjectName(V2IncusProjectPrefix, "acme", "backend")
	if err != nil {
		t.Fatal(err)
	}
	if name != "sc2-acme-backend" {
		t.Fatalf("name = %q, want sc2-acme-backend", name)
	}
}

func TestV2ProjectNameDefault(t *testing.T) {
	name, err := V2ProjectName(V2IncusProjectPrefix, "acme", DefaultProjectName)
	if err != nil {
		t.Fatal(err)
	}
	if name != "sc2-acme-default" {
		t.Fatalf("name = %q, want sc2-acme-default", name)
	}
}

func TestV2BridgeNameShortIsLiteral(t *testing.T) {
	name, err := V2BridgeName(V2IncusProjectPrefix, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if name != "sc2-acme" {
		t.Fatalf("name = %q, want sc2-acme", name)
	}
}

func TestV2BridgeNameLongIsHashedAndFits(t *testing.T) {
	long := "averylongtenanthandlethatexceedsfifteen"
	name, err := V2BridgeName(V2IncusProjectPrefix, long)
	if err != nil {
		t.Fatal(err)
	}
	if len(name) > 15 {
		t.Fatalf("bridge name %q exceeds 15 chars", name)
	}
	// stable
	again, err := V2BridgeName(V2IncusProjectPrefix, long)
	if err != nil {
		t.Fatal(err)
	}
	if name != again {
		t.Fatalf("bridge name not stable: %q vs %q", name, again)
	}
}

func TestV2ProjectNameRejectsReservedNothingButValidatesProject(t *testing.T) {
	if _, err := V2ProjectName(V2IncusProjectPrefix, "acme", "Bad"); err == nil {
		t.Fatal("expected error for invalid project name")
	}
}
