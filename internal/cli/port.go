package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
)

func newPortCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	port := &cobra.Command{
		Use:   "port",
		Short: "Manage sandbox app ports",
	}
	port.AddCommand(newPortSetCommand(config, opts))
	return port
}

func newPortSetCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "set project/name port",
		Short: "Set a sandbox app port",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPort, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("invalid app port %q", args[1])
			}
			plan, err := sandbox.PlanSetPort(cmd.Context(), config.adminConfig, config.projectStore, sandbox.PortSetRequest{
				Reference: args[0],
				AppPort:   appPort,
			})
			if err != nil {
				return err
			}
			if config.sandboxPort == nil {
				return fmt.Errorf("sandbox port executor is not configured")
			}
			if err := config.sandboxPort.SetAppPort(cmd.Context(), plan); err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatPortSetPlan(plan), plan)
		},
	}
}

func formatPortSetPlan(plan sandbox.PortSetPlan) string {
	return fmt.Sprintf("Set %s app port to %d", plan.Reference, plan.AppPort)
}
