package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/hostoverride"
)

func newHostCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "host",
		Short: "Manage local host overrides",
	}
	override := &cobra.Command{
		Use:   "override",
		Short: "Manage exact local host overrides",
	}
	override.AddCommand(newHostOverrideAddCommand(config, opts))
	command.AddCommand(override)
	return command
}

func newHostOverrideAddCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "add owner/project/name hostname",
		Short: "Plan a local exact host override",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := hostoverride.PlanAdd(cmd.Context(), config.adminConfig, config.projectStore, config.hostSandbox, hostoverride.AddRequest{
				Reference: args[0],
				Hostname:  args[1],
			})
			if err != nil {
				return err
			}
			if !dryRun {
				if config.hostOverrides == nil {
					return fmt.Errorf("host override executor is not configured")
				}
				if err := config.hostOverrides.Add(cmd.Context(), plan); err != nil {
					return err
				}
				if config.hostFiles == nil {
					return fmt.Errorf("host file editor is not configured")
				}
				if err := config.hostFiles.AddHostsEntry(cmd.Context(), plan); err != nil {
					return err
				}
			}
			return writeOutput(config.stdout, opts.output, formatHostOverrideAdd(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the host override plan without editing local state")
	return command
}

func formatHostOverrideAdd(plan hostoverride.AddPlan) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Host override: %s -> %s\n", plan.Hostname, plan.IPAddress)
	fmt.Fprintf(&builder, "%s\n%s\n%s\n", plan.HostsEntry.BeginLine, plan.HostsEntry.Line, plan.HostsEntry.EndLine)
	fmt.Fprintf(&builder, "Extra SANs: %s\n", strings.Join(plan.ExtraSANs, ","))
	fmt.Fprintf(&builder, "Warning: %s", plan.TrustWarning)
	return builder.String()
}
