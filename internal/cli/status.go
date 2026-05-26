package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
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
			status, err := tenant.GetStatusWithTopology(
				cmd.Context(),
				config.tenantStore,
				config.topologyStore,
				tenant.TopologyRequest{},
				reference,
			)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatTenantStatus(status), status)
		},
	}
}

func formatTenantStatus(status tenant.Status) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Tenant: %s\n", status.Summary.Tenant)
	fmt.Fprintf(&builder, "Incus project: %s\n", status.Summary.IncusName)
	fmt.Fprintf(&builder, "DNS suffix: %s\n", status.Summary.DNSSuffix)
	fmt.Fprintf(&builder, "Private CIDR: %s\n", status.Summary.PrivateCIDR)
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
