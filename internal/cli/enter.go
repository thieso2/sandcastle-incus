package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
)

func newEnterCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "connect [project/]machine [-- command...]",
		Aliases: []string{"c"},
		Short:   "Connect to a Sandcastle machine",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reference := args[0]
			command := args[1:]
			plan, err := sandbox.PlanEnter(cmd.Context(), config.adminConfig, config.projectStore, config.sandboxStore, sandbox.EnterRequest{
				Reference: reference,
				Command:   command,
			})
			if err != nil {
				if shouldCreateOnConnectFailure(err) {
					return createAndConnect(cmd, config, reference, command)
				}
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
				if shouldCreateOnConnectFailure(err) {
					return createAndConnect(cmd, config, reference, command)
				}
				return err
			}
			return nil
		},
	}
}

func shouldCreateOnConnectFailure(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "not found") && !strings.Contains(message, "project")
}

func createAndConnect(cmd *cobra.Command, config commandConfig, reference string, command []string) error {
	if config.stderr != nil {
		fmt.Fprintf(config.stderr, "Machine %s not found; creating it before connecting.\n", reference)
	}
	createPlan, err := sandbox.PlanCreate(cmd.Context(), config.adminConfig, config.projectStore, config.sandboxStore, sandbox.CreateRequest{
		Reference: reference,
	})
	if err != nil {
		return err
	}
	if config.sandboxCreator == nil {
		return fmt.Errorf("machine creation executor is not configured")
	}
	if err := config.sandboxCreator.CreateMachine(cmd.Context(), createPlan); err != nil {
		return err
	}
	if config.sandboxEnterer == nil {
		return fmt.Errorf("machine connect executor is not configured")
	}
	enterPlan, err := enterPlanFromCreatePlan(createPlan, command)
	if err != nil {
		return err
	}
	return config.sandboxEnterer.ConnectMachine(cmd.Context(), enterPlan, sandbox.EnterSession{
		Stdin:  config.stdin,
		Stdout: config.stdout,
		Stderr: config.stderr,
	})
}

func enterPlanFromCreatePlan(plan sandbox.CreatePlan, command []string) (sandbox.EnterPlan, error) {
	interactive := false
	if len(command) == 0 {
		command = []string{"/bin/bash", "-l"}
		interactive = true
	}
	if len(command) == 0 || command[0] == "" {
		return sandbox.EnterPlan{}, fmt.Errorf("enter command is required")
	}
	return sandbox.EnterPlan{
		Reference:    plan.Reference,
		Tenant:       plan.Tenant,
		Project:      plan.Project,
		Name:         plan.Name,
		InstanceName: plan.InstanceName,
		Command:      command,
		LinuxUser:    plan.LinuxUser,
		WorkingDir:   "/workspace",
		Interactive:  interactive,
	}, nil
}
