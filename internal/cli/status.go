package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/authapp"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/share"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func newStatusCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status [machine|tenant]",
		Short: "Show Sandcastle tenant or machine status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && args[0] != config.adminConfig.Tenant {
				result, err := machine.GetStatus(
					cmd.Context(),
					config.adminConfig,
					config.tenantStore,
					config.machineStore,
					machine.StatusRequest{Reference: args[0]},
				)
				if err == nil {
					return writeOutput(config.stdout, opts.output, formatMachineStatus(result), result)
				}
				if machine.IsAmbiguousMachineError(err) || strings.Contains(args[0], "/") || strings.Contains(args[0], ":") {
					return err
				}
			}
			reference := config.adminConfig.Tenant
			if len(args) == 1 {
				reference = args[0]
			}
			status, err := tenant.GetStatusWithTopologyForPrefix(
				cmd.Context(),
				config.tenantStore,
				config.topologyStore,
				tenant.TopologyRequest{},
				reference,
				installPrefixFromRemoteName(config.adminConfig.Remote, reference),
			)
			if err != nil {
				return err
			}
			addTenantShareReconciliationHealth(cmd.Context(), config, &status)
			return writeOutput(config.stdout, opts.output, formatTenantStatus(status), status)
		},
	}
}

func addTenantShareReconciliationHealth(ctx context.Context, config commandConfig, status *tenant.Status) {
	result, ok, err := tenantShareReconciliationDryRun(ctx, config, status.Summary)
	if !ok {
		return
	}
	if err != nil {
		status.Checks = append(status.Checks, tenant.Check{Name: "shares:reconcile", Status: "error", Detail: err.Error()})
		return
	}
	status.Shares.UnreconciledMachineCount = unreconciledShareMachineCount(result)
	detail := fmt.Sprintf("%d machine(s) checked", len(result.Machines))
	status.Checks = append(status.Checks, tenant.Check{Name: "shares:reconcile", Status: "ok", Detail: detail})
}

func tenantShareReconciliationDryRun(ctx context.Context, config commandConfig, summary tenant.Summary) (share.ReconcileResult, bool, error) {
	if config.shareReconciler != nil {
		result, err := config.shareReconciler.ReconcileTenantShares(ctx, summary, true)
		return result, true, err
	}
	if config.authShares != nil {
		result, err := config.authShares.ReconcileShares(ctx, authapp.ShareReconcileRequest{Tenant: summary.Tenant, DryRun: true})
		return result, true, err
	}
	if strings.TrimSpace(config.adminConfig.AuthToken) == "" || strings.TrimSpace(config.adminConfig.AuthHostname) == "" {
		return share.ReconcileResult{}, false, nil
	}
	client, err := shareClient(config)
	if err != nil {
		return share.ReconcileResult{}, false, nil
	}
	result, err := client.ReconcileShares(ctx, authapp.ShareReconcileRequest{Tenant: summary.Tenant, DryRun: true})
	return result, true, err
}

func unreconciledShareMachineCount(result share.ReconcileResult) int {
	count := 0
	for _, machine := range result.Machines {
		if machine.Changed || strings.TrimSpace(machine.Error) != "" {
			count++
		}
	}
	return count
}

func formatTenantStatus(status tenant.Status) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Tenant: %s\n", status.Summary.Tenant)
	fmt.Fprintf(&builder, "Incus project: %s\n", status.Summary.IncusName)
	fmt.Fprintf(&builder, "DNS suffix: %s\n", status.Summary.DNSSuffix)
	fmt.Fprintf(&builder, "Private CIDR: %s\n", status.Summary.PrivateCIDR)
	fmt.Fprintf(&builder, "Outbound shares: %d\n", status.Shares.OutboundShareCount)
	fmt.Fprintf(&builder, "Inbound accepted shares: %d\n", status.Shares.InboundAcceptedCount)
	fmt.Fprintf(&builder, "Pending inbound share offers: %d\n", status.Shares.PendingInboundOfferCount)
	fmt.Fprintf(&builder, "Unreconciled share machines: %d\n", status.Shares.UnreconciledMachineCount)
	for _, publicRoute := range status.Summary.PublicRoutes {
		fmt.Fprintf(&builder, "Route: %s -> %s/%s:%d\n", publicRoute.Hostname, publicRoute.Project, publicRoute.Machine, publicRoute.RoutePort)
	}
	for _, check := range status.Checks {
		if check.Detail == "" {
			fmt.Fprintf(&builder, "%s: %s\n", check.Name, check.Status)
			continue
		}
		fmt.Fprintf(&builder, "%s: %s (%s)\n", check.Name, check.Status, check.Detail)
	}
	return strings.TrimRight(builder.String(), "\n")
}
