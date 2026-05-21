package cli

import (
	"context"
	"fmt"

	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func refreshTenantDNS(ctx context.Context, config commandConfig, summary project.Summary) error {
	if config.dnsApplier == nil {
		return nil
	}
	if _, err := config.dnsApplier.Apply(ctx, dnsProject(summary)); err != nil {
		return fmt.Errorf("refresh tenant DNS: %w", err)
	}
	return nil
}
