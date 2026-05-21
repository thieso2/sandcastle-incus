package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
)

func newConnectCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "connect [project/]machine [-- command...]",
		Short: "Connect to a Sandcastle machine",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := machine.PlanConnect(cmd.Context(), config.adminConfig, config.tenantStore, config.machineStore, machine.ConnectRequest{
				Reference: args[0],
				Command:   args[1:],
			})
			if err != nil {
				return err
			}
			if config.machineConnector == nil {
				return fmt.Errorf("machine connect executor is not configured")
			}
			if err := config.machineConnector.ConnectMachine(cmd.Context(), plan, machine.ConnectSession{
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
