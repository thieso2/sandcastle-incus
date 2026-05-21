package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
)

func newSandboxLifecycleCommand(config commandConfig, opts *rootOptions, use string, action sandbox.Action, requireYes bool) *cobra.Command {
	var yes bool
	command := &cobra.Command{
		Use:   use + " project/name",
		Short: string(action) + " a Sandcastle sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if requireYes && !yes {
				return fmt.Errorf("refusing to remove sandbox without --yes")
			}
			plan, err := sandbox.PlanLifecycle(cmd.Context(), config.adminConfig, config.projectStore, sandbox.LifecycleRequest{
				Reference: args[0],
				Action:    action,
			})
			if err != nil {
				return err
			}
			if config.sandboxControl == nil {
				return fmt.Errorf("sandbox lifecycle executor is not configured")
			}
			if err := config.sandboxControl.ApplyLifecycle(cmd.Context(), plan); err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatLifecyclePlan(plan), plan)
		},
	}
	if requireYes {
		command.Flags().BoolVar(&yes, "yes", false, "confirm sandbox removal")
	}
	return command
}

func formatLifecyclePlan(plan sandbox.LifecyclePlan) string {
	return fmt.Sprintf("%s %s", plan.Action, plan.Reference)
}
