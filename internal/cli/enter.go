package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
)

func newEnterCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "connect [project/]machine [-- command...]",
		Short: "Connect to a Sandcastle machine",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := sandbox.PlanEnter(cmd.Context(), config.adminConfig, config.projectStore, sandbox.EnterRequest{
				Reference: args[0],
				Command:   args[1:],
			})
			if err != nil {
				return err
			}
			if config.sandboxEnterer == nil {
				return fmt.Errorf("machine connect executor is not configured")
			}
			if err := config.sandboxEnterer.ConnectMachine(cmd.Context(), plan, sandbox.EnterSession{
				Stdin:  config.stdin,
				Stdout: config.stdout,
				Stderr: config.stderr,
			}); err != nil {
				return err
			}
			return nil
		},
	}
}
