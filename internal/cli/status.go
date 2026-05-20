package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

func newStatusCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status project",
		Short: "Show Sandcastle project status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := naming.ParseProjectRefWithDefaultOwner(args[0], config.adminConfig.Owner)
			if err != nil {
				return err
			}
			status, err := project.GetStatusWithTopology(
				cmd.Context(),
				config.projectStore,
				config.topologyStore,
				project.TopologyRequest{StoragePool: config.adminConfig.StoragePool},
				ref.String(),
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
	fmt.Fprintf(&builder, "Project: %s/%s\n", status.Summary.Owner, status.Summary.Name)
	fmt.Fprintf(&builder, "Incus project: %s\n", status.Summary.IncusName)
	fmt.Fprintf(&builder, "Domain: %s\n", status.Summary.Domain)
	fmt.Fprintf(&builder, "Private CIDR: %s\n", status.Summary.PrivateCIDR)
	for _, check := range status.Checks {
		if check.Detail == "" {
			fmt.Fprintf(&builder, "%s: %s\n", check.Name, check.Status)
			continue
		}
		fmt.Fprintf(&builder, "%s: %s (%s)\n", check.Name, check.Status, check.Detail)
	}
	return strings.TrimRight(builder.String(), "\n")
}
