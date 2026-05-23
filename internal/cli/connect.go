package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
)

func newConnectCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "connect [project/]machine [-- command...]",
		Aliases: []string{"c"},
		Short:   "Connect to a Sandcastle machine",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reference := args[0]
			command := args[1:]
			plan, err := machine.PlanConnect(cmd.Context(), config.adminConfig, config.tenantStore, config.machineStore, machine.ConnectRequest{
				Reference: reference,
				Command:   command,
			})
			if err != nil {
				if shouldCreateOnConnectFailure(err) {
					return createAndConnect(cmd, config, reference, command)
				}
				return err
			}
			if config.machineConnector == nil {
				return fmt.Errorf("machine connect executor is not configured")
			}
			if err := refreshKnownHostsForPrivateIPConnect(cmd.Context(), config, plan); err != nil {
				return err
			}
			if err := config.machineConnector.ConnectMachine(cmd.Context(), plan, machine.ConnectSession{
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

func refreshKnownHostsForPrivateIPConnect(ctx context.Context, config commandConfig, plan machine.ConnectPlan) error {
	if !plan.Managed || strings.TrimSpace(plan.PrivateIP) == "" || plan.SSHHost != plan.PrivateIP {
		return nil
	}
	return refreshMachineKnownHosts(ctx, config, machine.CreatePlan{
		Tenant:       plan.Tenant,
		Project:      plan.Project,
		Name:         plan.Name,
		InstanceName: plan.InstanceName,
		Hostname:     plan.Hostname,
		PrivateIP:    plan.PrivateIP,
	})
}

func createAndConnect(cmd *cobra.Command, config commandConfig, reference string, command []string) error {
	if config.stderr != nil {
		fmt.Fprintf(config.stderr, "Machine %s not found; creating it before connecting.\n", reference)
	}
	createPlan, err := machine.PlanCreate(cmd.Context(), config.adminConfig, tenantStoreWithSSHKeyMetadata(config.tenantStore), config.machineStore, machine.CreateRequest{
		Reference: reference,
	})
	if err != nil {
		return err
	}
	if config.machineCreator == nil {
		return fmt.Errorf("machine creation executor is not configured")
	}
	if err := config.machineCreator.CreateMachine(cmd.Context(), createPlan); err != nil {
		return err
	}
	if err := refreshTenantDNS(cmd.Context(), config, createPlan.Tenant); err != nil {
		return err
	}
	if err := refreshMachineKnownHosts(cmd.Context(), config, createPlan); err != nil {
		return err
	}
	if config.machineConnector == nil {
		return fmt.Errorf("machine connect executor is not configured")
	}
	connectPlan, err := connectPlanFromCreatePlan(createPlan, command)
	if err != nil {
		return err
	}
	return config.machineConnector.ConnectMachine(cmd.Context(), connectPlan, machine.ConnectSession{
		Stdin:  config.stdin,
		Stdout: config.stdout,
		Stderr: config.stderr,
	})
}

func connectPlanFromCreatePlan(plan machine.CreatePlan, command []string) (machine.ConnectPlan, error) {
	interactive := false
	if len(command) == 0 {
		command = []string{"/bin/bash", "-l"}
		interactive = true
	}
	if len(command) == 0 || command[0] == "" {
		return machine.ConnectPlan{}, fmt.Errorf("connect command is required")
	}
	return machine.ConnectPlan{
		Reference:    plan.Reference,
		Tenant:       plan.Tenant,
		Project:      plan.Project,
		Name:         plan.Name,
		InstanceName: plan.InstanceName,
		Hostname:     plan.Hostname,
		PrivateIP:    plan.PrivateIP,
		SSHHost:      plan.PrivateIP,
		HostKeyAlias: plan.Hostname,
		Command:      command,
		LinuxUser:    plan.LinuxUser,
		UserID:       machine.DefaultLinuxUID,
		GroupID:      machine.DefaultLinuxGID,
		WorkingDir:   "/workspace",
		Interactive:  interactive,
		Managed:      true,
	}, nil
}
