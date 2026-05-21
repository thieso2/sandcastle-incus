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
	override.AddCommand(newHostOverrideDeleteCommand(config, opts))
	command.AddCommand(override)
	return command
}

func newHostOverrideAddCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "create [project/]machine hostname",
		Short: "Create a local exact host override",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := hostoverride.PlanAdd(cmd.Context(), config.adminConfig, config.tenantStore, config.hostMachine, hostoverride.AddRequest{
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
		Use:   "list [tenant]",
		Short: "List local host override metadata",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var reference string
			if len(args) > 0 {
				reference = args[0]
			}
			result, err := hostoverride.PlanList(cmd.Context(), config.adminConfig, config.tenantStore, config.hostMachine, hostoverride.ListRequest{Reference: reference})
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatHostOverrideList(result), result)
		},
	}
}

func newHostOverrideDeleteCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "delete [project/]machine hostname",
		Short: "Delete a local exact host override",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := hostoverride.PlanDelete(cmd.Context(), config.adminConfig, config.tenantStore, config.hostMachine, hostoverride.DeleteRequest{
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
				if err := config.hostOverrides.Delete(cmd.Context(), plan); err != nil {
					return err
				}
				if config.hostFiles == nil {
					return fmt.Errorf("host file editor is not configured")
				}
				if err := config.hostFiles.RemoveHostsEntry(cmd.Context(), plan); err != nil {
					return err
				}
			}
			return writeOutput(config.stdout, opts.output, formatHostOverrideDelete(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the host override delete plan without editing local state")
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

func formatHostOverrideDelete(plan hostoverride.DeletePlan) string {
	return fmt.Sprintf("Delete host override: %s from %s", plan.Hostname, plan.Reference)
}
