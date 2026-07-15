package authapp

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/svclog"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

// suffixClaimReconcileInterval is deliberately slow — DNS-suffix claims only
// churn on tenant create/delete, which is rare, and the claim's own UNIQUE
// constraint already guarantees correctness; this loop just garbage-collects
// claims orphaned by a tenant deleted out-of-band (e.g. `sc-adm tenant delete`,
// which runs against Incus and cannot reach the auth database).
const suffixClaimReconcileInterval = 5 * time.Minute

// pruneOrphanSuffixClaims releases every claim whose tenant is absent from live,
// with one guard: an EMPTY live set is never trusted. A genuinely empty install
// has no claims worth pruning, and refusing to prune here means a transient
// tenant-listing hiccup can never wipe the whole registry. Returns the count
// pruned.
func pruneOrphanSuffixClaims(ctx context.Context, db *sql.DB, live []string) (int, error) {
	if db == nil {
		return 0, nil
	}
	if len(live) == 0 {
		return 0, nil
	}
	return ReconcileDNSSuffixClaims(ctx, db, live)
}

// reconcileSuffixClaimsOnce lists the install's live tenants and prunes orphaned
// claims. A listing error aborts WITHOUT pruning (the guard above only covers an
// empty result; a hard error must not be read as "no tenants").
func (r HTTPRunner) reconcileSuffixClaimsOnce(ctx context.Context, db *sql.DB) (int, error) {
	if db == nil || r.Tenants == nil {
		return 0, nil
	}
	summaries, err := tenant.ListForPrefix(ctx, r.Tenants, r.Admin.IncusProjectPrefix)
	if err != nil {
		return 0, fmt.Errorf("list tenants for DNS suffix reconcile: %w", err)
	}
	live := make([]string, 0, len(summaries))
	for _, s := range summaries {
		if name := strings.TrimSpace(s.Tenant); name != "" {
			live = append(live, name)
		}
	}
	return pruneOrphanSuffixClaims(ctx, db, live)
}

// runSuffixClaimReconcileLoop prunes orphaned DNS-suffix claims once at startup
// and then every suffixClaimReconcileInterval, until ctx is cancelled. Errors
// are logged and the loop continues.
func (r HTTPRunner) runSuffixClaimReconcileLoop(ctx context.Context, db *sql.DB, logger *svclog.Logger) {
	run := func() {
		pruned, err := r.reconcileSuffixClaimsOnce(ctx, db)
		if err != nil {
			logger.Message(ctx, "ERROR", "auth-app DNS suffix claim reconcile: %v", err)
			return
		}
		if pruned > 0 {
			logger.Message(ctx, "INFO", "auth-app pruned %d orphaned DNS suffix claim(s)", pruned)
		}
	}
	run()
	ticker := time.NewTicker(suffixClaimReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
