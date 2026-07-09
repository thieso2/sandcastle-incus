package cli

import (
	"fmt"

	"github.com/spf13/cobra"
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
			summary, err := requireV2Tenant(cmd.Context(), config)
			if err != nil {
				return err
			}
			project, machineName, err := resolveV2MachineTarget(cmd.Context(), config, summary, args[0])
			if err != nil {
				return err
			}
			if requireYes && !yes {
				confirmed, err := confirmMissingYes(config, "Delete machine "+project+":"+machineName+"?", "refusing to delete machine without --yes")
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
