package authapp

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func newClaimsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := OpenDatabase(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestClaimDNSSuffix_NewClaimStoresAndReads(t *testing.T) {
	ctx := context.Background()
	db := newClaimsTestDB(t)

	got, err := ClaimDNSSuffix(ctx, db, "castle", "e2edns", "e2edns")
	if err != nil {
		t.Fatal(err)
	}
	if got != "castle" {
		t.Fatalf("claimed suffix = %q, want castle", got)
	}
	suffix, found, err := GetDNSSuffixClaimByTenant(ctx, db, "e2edns")
	if err != nil {
		t.Fatal(err)
	}
	if !found || suffix != "castle" {
		t.Fatalf("GetByTenant = %q,%v want castle,true", suffix, found)
	}
}

func TestClaimDNSSuffix_ReclaimSameIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := newClaimsTestDB(t)

	if _, err := ClaimDNSSuffix(ctx, db, "castle", "e2edns", "e2edns"); err != nil {
		t.Fatal(err)
	}
	// Re-login: the same tenant re-claims the same suffix -> no error.
	if _, err := ClaimDNSSuffix(ctx, db, "castle", "e2edns", "e2edns"); err != nil {
		t.Fatalf("re-claiming the same suffix should be idempotent, got %v", err)
	}
}

func TestClaimDNSSuffix_TakenByAnotherTenant(t *testing.T) {
	ctx := context.Background()
	db := newClaimsTestDB(t)

	if _, err := ClaimDNSSuffix(ctx, db, "castle", "alice", "alice"); err != nil {
		t.Fatal(err)
	}
	_, err := ClaimDNSSuffix(ctx, db, "castle", "bob", "bob")
	var claimErr *SuffixClaimError
	if !errors.As(err, &claimErr) || !claimErr.Taken {
		t.Fatalf("want SuffixClaimError{Taken:true}, got %v", err)
	}
}

func TestClaimDNSSuffix_ImmutablePerTenant(t *testing.T) {
	ctx := context.Background()
	db := newClaimsTestDB(t)

	if _, err := ClaimDNSSuffix(ctx, db, "castle", "e2edns", "e2edns"); err != nil {
		t.Fatal(err)
	}
	_, err := ClaimDNSSuffix(ctx, db, "other", "e2edns", "e2edns")
	var claimErr *SuffixClaimError
	if !errors.As(err, &claimErr) || claimErr.Taken || claimErr.Existing != "castle" {
		t.Fatalf("want SuffixClaimError{immutable, Existing:castle}, got %v", err)
	}
}

func TestClaimDNSSuffix_NormalizesCase(t *testing.T) {
	ctx := context.Background()
	db := newClaimsTestDB(t)

	got, err := ClaimDNSSuffix(ctx, db, "  Castle ", "alice", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got != "castle" {
		t.Fatalf("normalized suffix = %q, want castle", got)
	}
	// Case-insensitive uniqueness: another tenant can't take "CASTLE".
	if _, err := ClaimDNSSuffix(ctx, db, "CASTLE", "bob", "bob"); err == nil {
		t.Fatal("expected case-insensitive collision to be rejected")
	}
}

func TestReleaseDNSSuffixClaim_FreesTheSuffix(t *testing.T) {
	ctx := context.Background()
	db := newClaimsTestDB(t)

	if _, err := ClaimDNSSuffix(ctx, db, "castle", "alice", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseDNSSuffixClaim(ctx, db, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := GetDNSSuffixClaimByTenant(ctx, db, "alice"); found {
		t.Fatal("claim should be gone after release")
	}
	// Reusable by another tenant now.
	if _, err := ClaimDNSSuffix(ctx, db, "castle", "bob", "bob"); err != nil {
		t.Fatalf("released suffix should be reusable, got %v", err)
	}
}

func TestReconcileDNSSuffixClaims_PrunesOrphans(t *testing.T) {
	ctx := context.Background()
	db := newClaimsTestDB(t)

	if _, err := ClaimDNSSuffix(ctx, db, "castle", "e2edns", "e2edns"); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimDNSSuffix(ctx, db, "ghost", "gone", "gone"); err != nil {
		t.Fatal(err)
	}
	pruned, err := ReconcileDNSSuffixClaims(ctx, db, []string{"e2edns"})
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1", pruned)
	}
	if _, found, _ := GetDNSSuffixClaimByTenant(ctx, db, "gone"); found {
		t.Fatal("orphaned claim should be pruned")
	}
	if _, found, _ := GetDNSSuffixClaimByTenant(ctx, db, "e2edns"); !found {
		t.Fatal("live tenant's claim must survive reconcile")
	}
}
