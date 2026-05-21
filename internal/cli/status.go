package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func newStatusCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status project",
		Short: "Show Sandcastle project status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			status, err := project.GetStatusWithTopology(
				cmd.Context(),
				config.projectStore,
				config.topologyStore,
				project.TopologyRequest{},
				args[0],
			)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatProjectStatus(status), status)
		},
	}
}

func formatProjectStatus(status project.Status) string {
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
