package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
)

func newMachineLifecycleCommand(config commandConfig, opts *rootOptions, use string, action machine.Action, requireYes bool) *cobra.Command {
	var yes bool
	command := &cobra.Command{
		Use:   use + " [tenant/][project:]machine",
		Short: machineLifecycleShort(action),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if requireYes && !yes && !isTerminalInput(config) {
				return fmt.Errorf("refusing to delete machine without --yes")
			}
			// v2 tenants: freeform instances, plain names — apply the action
			// directly instead of the v1 plan machinery.
			if summary, isV2 := v2TenantSummary(cmd.Context(), config); isV2 {
				project, machineName, err := resolveV2MachineReference(summary, args[0], config.adminConfig.Project)
				if err != nil {
					return err
				}
				if requireYes && !yes {
					confirmed, err := confirmMissingYes(config, "Delete machine "+machineName+"?", "refusing to delete machine without --yes")
					if err != nil {
						return err
					}
					if !confirmed {
						return fmt.Errorf("delete canceled")
					}
				}
				if err := config.tenantCreator.MachineLifecycleV2(cmd.Context(), summary.V2IncusProjectName(project), machineName, string(action)); err != nil {
					return err
				}
				payload := struct {
					Action  string `json:"action"`
					Tenant  string `json:"tenant"`
					Project string `json:"project"`
					Machine string `json:"machine"`
				}{string(action), summary.Tenant, project, machineName}
				return writeOutput(config.stdout, opts.output, fmt.Sprintf("%s %s", action, machineName), payload)
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
			if requireYes && !yes {
				confirmed, err := confirmMissingYes(config, "Delete machine "+plan.Reference+"?", "refusing to delete machine without --yes")
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("delete canceled")
				}
			}
			if err := config.machineControl.ApplyLifecycle(cmd.Context(), plan); err != nil {
				return err
			}
			if plan.Action == machine.ActionDelete {
				cache := incusx.NewConnectCache(config.adminConfig.Remote)
				if key := connectPlanCacheKey(plan.Tenant.Tenant, plan.Project, plan.Name); key != "" {
					cache.InvalidatePlan(key)
				}
				cache.InvalidateKeyscan(machine.MachineHostname(plan.Name, plan.Project, plan.Tenant.DNSSuffix))
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
