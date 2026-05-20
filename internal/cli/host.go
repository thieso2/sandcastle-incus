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
	override.AddCommand(newHostOverrideListCommand(config, opts))
	override.AddCommand(newHostOverrideRemoveCommand(config, opts))
	command.AddCommand(override)
	return command
}

func newHostOverrideAddCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "add project/name hostname",
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

func newHostOverrideListCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "list project",
		Short: "List local host override metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := hostoverride.PlanList(cmd.Context(), config.adminConfig, config.projectStore, config.hostSandbox, hostoverride.ListRequest{Reference: args[0]})
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatHostOverrideList(result), result)
		},
	}
}

func newHostOverrideRemoveCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "rm project/name hostname",
		Short: "Remove a local exact host override",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := hostoverride.PlanRemove(cmd.Context(), config.adminConfig, config.projectStore, config.hostSandbox, hostoverride.RemoveRequest{
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
				if err := config.hostOverrides.Remove(cmd.Context(), plan); err != nil {
					return err
				}
				if config.hostFiles == nil {
					return fmt.Errorf("host file editor is not configured")
				}
				if err := config.hostFiles.RemoveHostsEntry(cmd.Context(), plan); err != nil {
					return err
				}
			}
			return writeOutput(config.stdout, opts.output, formatHostOverrideRemove(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the host override removal plan without editing local state")
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

func formatHostOverrideList(result hostoverride.ListResult) string {
	if len(result.Overrides) == 0 {
		return "No host overrides"
	}
	var builder strings.Builder
	for _, override := range result.Overrides {
		fmt.Fprintf(&builder, "%s %s -> %s\n", override.Reference, override.Hostname, override.IPAddress)
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

func formatHostOverrideRemove(plan hostoverride.RemovePlan) string {
	return fmt.Sprintf("Remove host override: %s from %s", plan.Hostname, plan.Reference)
}
