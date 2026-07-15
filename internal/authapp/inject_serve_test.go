package authapp

import (
	"context"
	"path/filepath"
	"testing"
)

func TestInjectServeDependencies_PatchesConcreteProvisioner(t *testing.T) {
	db := newClaimsTestDB(t)

	got := injectServeDependencies(Provisioner{}, db, "coder")
	typed, ok := got.(Provisioner)
	if !ok {
		t.Fatalf("expected a Provisioner, got %T", got)
	}
	if typed.DB != db {
		t.Fatal("DB was not injected — first-login suffix claims would silently no-op")
	}
	if typed.DefaultUnixUser != "coder" {
		t.Fatalf("DefaultUnixUser = %q, want coder", typed.DefaultUnixUser)
	}
}

func TestInjectServeDependencies_KeepsExplicitUnixUser(t *testing.T) {
	db := newClaimsTestDB(t)
	got := injectServeDependencies(Provisioner{DefaultUnixUser: "custom"}, db, "coder")
	if got.(Provisioner).DefaultUnixUser != "custom" {
		t.Fatal("an explicit DefaultUnixUser must not be overwritten")
	}
}

func TestInjectServeDependencies_LeavesFakesUnchanged(t *testing.T) {
	fake := &fakePersonalTenantProvisioner{}
	if got := injectServeDependencies(fake, nil, "coder"); got != fake {
		t.Fatal("a non-Provisioner value must pass through unchanged")
	}
}

// The claim wiring itself is covered by the store tests; this guards that the
// Provisioner surfaces the resolved suffix it will claim on.
func TestClaimDNSSuffix_IsReadBackForRelogin(t *testing.T) {
	ctx := context.Background()
	db, err := OpenDatabase(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimDNSSuffix(ctx, db, "castle", "e2edns", "e2edns"); err != nil {
		t.Fatal(err)
	}
	// Re-login path: the server reads the stored suffix back for the tenant.
	suffix, found, err := GetDNSSuffixClaimByTenant(ctx, db, "e2edns")
	if err != nil || !found || suffix != "castle" {
		t.Fatalf("re-login read-back = %q,%v,%v want castle,true,nil", suffix, found, err)
	}
}
