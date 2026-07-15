package authapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// The dns_suffix_claims table is the per-install uniqueness registry for Tenant
// DNS Suffixes (ADR-0020). The suffix is the stem of every incus remote name and
// must be unique on the install; the `suffix` PRIMARY KEY gives atomic
// first-come-first-served, and the `tenant` UNIQUE constraint enforces one
// immutable suffix per tenant. The suffix *value* is also mirrored into the
// tenant's Incus infra-project config (meta.KeyV2Suffix); this table exists so
// uniqueness is a real DB constraint rather than a racy scan.

// SuffixClaimError explains why a ClaimDNSSuffix call was rejected. Taken means
// another tenant already holds the suffix on this install; otherwise the calling
// tenant already holds a different (immutable) suffix, carried in Existing.
type SuffixClaimError struct {
	Suffix   string
	Existing string
	Taken    bool
}

func (e *SuffixClaimError) Error() string {
	if e.Taken {
		return fmt.Sprintf("DNS suffix %q is already claimed on this install", e.Suffix)
	}
	return fmt.Sprintf("this tenant already has DNS suffix %q (a suffix is immutable once chosen)", e.Existing)
}

// normalizeDNSSuffix lowercases and trims a suffix so uniqueness is
// case-insensitive and whitespace-insensitive.
func normalizeDNSSuffix(suffix string) string {
	return strings.ToLower(strings.TrimSpace(suffix))
}

// ClaimDNSSuffix reserves suffix for tenant, first-come-first-served. The INSERT
// is the atomic gate; a conflict is then classified into a SuffixClaimError. A
// tenant re-claiming the suffix it already holds is a no-op (re-login). Returns
// the normalized suffix that was (or is already) claimed.
func ClaimDNSSuffix(ctx context.Context, db *sql.DB, suffix, tenant, userKey string) (string, error) {
	norm := normalizeDNSSuffix(suffix)
	tenant = strings.TrimSpace(tenant)
	if norm == "" {
		return "", fmt.Errorf("DNS suffix is required")
	}
	if tenant == "" {
		return "", fmt.Errorf("tenant is required to claim a DNS suffix")
	}
	_, err := db.ExecContext(ctx, `
INSERT INTO dns_suffix_claims (suffix, tenant, user_key, created_at, updated_at)
VALUES (?, ?, ?, datetime('now'), datetime('now'))
`, norm, tenant, strings.TrimSpace(userKey))
	if err == nil {
		return norm, nil
	}
	// A conflict fired on either the suffix PK or the tenant UNIQUE. Classify by
	// what the tenant currently holds; correctness (FCFS) rests on the INSERT, so
	// this follow-up only shapes the error message.
	existing, found, gerr := GetDNSSuffixClaimByTenant(ctx, db, tenant)
	if gerr != nil {
		return "", fmt.Errorf("claim DNS suffix: %w", err)
	}
	if found {
		if existing == norm {
			return norm, nil // idempotent re-claim (re-login)
		}
		return "", &SuffixClaimError{Existing: existing}
	}
	// The tenant holds nothing, so the collision is another tenant's suffix.
	return "", &SuffixClaimError{Suffix: norm, Taken: true}
}

// GetDNSSuffixClaimByTenant returns the suffix a tenant currently holds, if any.
func GetDNSSuffixClaimByTenant(ctx context.Context, db *sql.DB, tenant string) (string, bool, error) {
	var suffix string
	err := db.QueryRowContext(ctx, `
SELECT suffix FROM dns_suffix_claims WHERE tenant = ?
`, strings.TrimSpace(tenant)).Scan(&suffix)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("look up DNS suffix claim: %w", err)
	}
	return suffix, true, nil
}

// ReleaseDNSSuffixClaim frees a tenant's suffix (called on tenant deletion).
func ReleaseDNSSuffixClaim(ctx context.Context, db *sql.DB, tenant string) error {
	if _, err := db.ExecContext(ctx, `
DELETE FROM dns_suffix_claims WHERE tenant = ?
`, strings.TrimSpace(tenant)); err != nil {
		return fmt.Errorf("release DNS suffix claim: %w", err)
	}
	return nil
}

// ReconcileDNSSuffixClaims prunes claims whose tenant is no longer live in Incus
// (out-of-band deletions), keeping Incus the source of truth for tenant
// existence. Returns the number of claims pruned.
func ReconcileDNSSuffixClaims(ctx context.Context, db *sql.DB, liveTenants []string) (int, error) {
	live := make(map[string]struct{}, len(liveTenants))
	for _, t := range liveTenants {
		live[strings.TrimSpace(t)] = struct{}{}
	}
	rows, err := db.QueryContext(ctx, `SELECT tenant FROM dns_suffix_claims`)
	if err != nil {
		return 0, fmt.Errorf("list DNS suffix claims: %w", err)
	}
	var orphans []string
	for rows.Next() {
		var tenant string
		if err := rows.Scan(&tenant); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan DNS suffix claim: %w", err)
		}
		if _, ok := live[tenant]; !ok {
			orphans = append(orphans, tenant)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate DNS suffix claims: %w", err)
	}
	rows.Close()
	for _, tenant := range orphans {
		if err := ReleaseDNSSuffixClaim(ctx, db, tenant); err != nil {
			return 0, err
		}
	}
	return len(orphans), nil
}
