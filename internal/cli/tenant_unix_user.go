package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/naming"
)

func ensureTenantUnixUserForMachineCreate(ctx context.Context, config commandConfig) error {
	localUser := strings.TrimSpace(defaultLocalUnixUsername())
	if localUser == "" {
		verboseCLI(config, "tenant unix user: local OS username is empty; keeping tenant metadata unchanged")
		return nil
	}
	if err := naming.ValidateUnixUsername(localUser); err != nil {
		return fmt.Errorf("local OS username %q cannot be used as machine Unix user: %w", localUser, err)
	}
	summary, err := currentTenantSummary(ctx, config)
	if err != nil {
		return err
	}
	if summary.UnixUser != "" && summary.UnixUser != summary.Tenant {
		verboseCLI(config, "tenant unix user: keeping existing tenant Unix user %q for tenant %s", summary.UnixUser, summary.Tenant)
		return nil
	}
	if summary.UnixUser == localUser {
		return nil
	}
	if config.tenantUnixUser == nil {
		verboseCLI(config, "tenant unix user: cannot set tenant %s to local user %q; updater is not configured", summary.Tenant, localUser)
		return nil
	}
	verboseCLI(config, "tenant unix user: setting tenant %s Unix user to local user %q", summary.Tenant, localUser)
	return config.tenantUnixUser.SetTenantUnixUser(ctx, summary.IncusName, localUser)
}
