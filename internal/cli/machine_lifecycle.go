package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
)

func newMachineLifecycleCommand(config commandConfig, opts *rootOptions, use string, action machine.Action, requireYes bool) *cobra.Command {
	var yes bool
	command := &cobra.Command{
		Use:   use + " [project/]machine",
		Short: machineLifecycleShort(action),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if requireYes && !yes {
				return fmt.Errorf("refusing to delete machine without --yes")
			}
			plan, err := machine.PlanLifecycle(cmd.Context(), config.adminConfig, config.tenantStore, config.machineStore, machine.LifecycleRequest{
				Reference: args[0],
				Action:    action,
			})
			if err != nil {
				return err
			}
			if config.machineControl == nil {
				return fmt.Errorf("machine lifecycle executor is not configured")
			}
			if err := config.machineControl.ApplyLifecycle(cmd.Context(), plan); err != nil {
				return err
			}
			if plan.Action == machine.ActionDelete {
				if err := refreshTenantDNS(cmd.Context(), config, plan.Tenant); err != nil {
					return err
				}
			}
			return writeOutput(config.stdout, opts.output, formatLifecyclePlan(plan), plan)
		},
	}
	if requireYes {
		command.Flags().BoolVar(&yes, "yes", false, "confirm machine deletion")
	}
	return command
}

func machineLifecycleShort(action machine.Action) string {
	switch action {
	case machine.ActionStart:
		return "Start a Sandcastle machine"
	case machine.ActionStop:
		return "Stop a Sandcastle machine"
	case machine.ActionRestart:
		return "Restart a Sandcastle machine"
	case machine.ActionDelete:
		return "Delete a Sandcastle machine"
	default:
		return string(action) + " a Sandcastle machine"
	}
}

func formatLifecyclePlan(plan machine.LifecyclePlan) string {
	return fmt.Sprintf("%s %s", plan.Action, plan.Reference)
}
