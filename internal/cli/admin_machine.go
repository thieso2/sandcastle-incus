package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/naming"
)

func newAdminMachineListCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "list tenant[/project]",
		Short: "List Sandcastle machines in a tenant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, projectName, err := adminMachineListConfig(config, args[0])
			if err != nil {
				return err
			}
			result, err := listMachines(cmd.Context(), cfg, listMachinesRequest{
				Project:     projectName,
				AllProjects: projectName == "",
			})
			if err != nil {
				return err
			}
			return writeOutput(cfg.stdout, opts.output, formatMachineList(result), result)
		},
	}
	return command
}

func newAdminMachineCreateCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	var detach bool
	var template string
	var appPort int
	var homeDir string
	var workspaceDir string
	var shareHome bool
	var containerTools bool
	command := &cobra.Command{
		Use:   "create tenant[/project]/machine",
		Short: "Create a Sandcastle machine in any tenant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, userRef, adminRef, err := adminMachineConfig(config, args[0])
			if err != nil {
				return err
			}
			plan, err := sandbox.PlanCreate(cmd.Context(), cfg.adminConfig, cfg.projectStore, cfg.sandboxStore, sandbox.CreateRequest{
				Reference:      userRef,
				Template:       template,
				AppPort:        appPort,
				HomeDir:        homeDir,
				WorkspaceDir:   workspaceDir,
				ShareHome:      shareHome,
				ContainerTools: containerTools,
			})
			if err != nil {
				return err
			}
			plan.Reference = adminRef
			if !dryRun {
				if cfg.sandboxCreator == nil {
					return fmt.Errorf("machine creation executor is not configured")
				}
				if err := cfg.sandboxCreator.CreateMachine(cmd.Context(), plan); err != nil {
					return err
				}
				if err := refreshTenantDNS(cmd.Context(), cfg, plan.Tenant); err != nil {
					return err
				}
				if !detach {
					if cfg.sandboxEnterer == nil {
						return fmt.Errorf("machine connect executor is not configured")
					}
					enterPlan, err := sandbox.PlanEnter(cmd.Context(), cfg.adminConfig, cfg.projectStore, cfg.sandboxStore, sandbox.EnterRequest{Reference: userRef})
					if err != nil {
						return err
					}
					enterPlan.Reference = adminRef
					if err := cfg.sandboxEnterer.ConnectMachine(cmd.Context(), enterPlan, sandbox.EnterSession{
						Stdin:  cfg.stdin,
						Stdout: cfg.stdout,
						Stderr: cfg.stderr,
					}); err != nil {
						return err
					}
				}
			}
			return writeOutput(cfg.stdout, opts.output, formatSandboxPlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the machine creation plan without creating a container")
	command.Flags().BoolVar(&detach, "detach", false, "create the machine without connecting to it")
	command.Flags().BoolVar(&detach, "background", false, "create the machine without connecting to it")
	command.Flags().StringVar(&template, "template", "", "machine template to use (ai or base)")
	command.Flags().IntVar(&appPort, "app-port", 0, "application port proxied by machine Caddy")
	command.Flags().StringVar(&homeDir, "home-dir", "", "project home volume subdirectory")
	command.Flags().StringVar(&workspaceDir, "workspace-dir", "", "project workspace volume subdirectory")
	command.Flags().BoolVar(&shareHome, "share-home", false, "confirm sharing a home subdirectory with another running machine")
	command.Flags().BoolVar(&containerTools, "container-tools", false, "enable nested container tooling for this machine")
	return command
}

func newAdminMachineConnectCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "connect tenant[/project]/machine [-- command...]",
		Short: "Connect to a Sandcastle machine in any tenant",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, userRef, adminRef, err := adminMachineConfig(config, args[0])
			if err != nil {
				return err
			}
			plan, err := sandbox.PlanEnter(cmd.Context(), cfg.adminConfig, cfg.projectStore, cfg.sandboxStore, sandbox.EnterRequest{
				Reference: userRef,
				Command:   args[1:],
			})
			if err != nil {
				return err
			}
			plan.Reference = adminRef
			if cfg.sandboxEnterer == nil {
				return fmt.Errorf("machine connect executor is not configured")
			}
			return cfg.sandboxEnterer.ConnectMachine(cmd.Context(), plan, sandbox.EnterSession{
				Stdin:  cfg.stdin,
				Stdout: cfg.stdout,
				Stderr: cfg.stderr,
			})
		},
	}
}

func newAdminMachineStatusCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status tenant[/project]/machine",
		Short: "Show Sandcastle machine status in any tenant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, userRef, adminRef, err := adminMachineConfig(config, args[0])
			if err != nil {
				return err
			}
			result, err := sandbox.Inspect(cmd.Context(), cfg.adminConfig, cfg.projectStore, cfg.sandboxStore, sandbox.InspectRequest{Reference: userRef})
			if err != nil {
				return err
			}
			result.Reference = adminRef
			return writeOutput(cfg.stdout, opts.output, formatSandboxInspect(result), result)
		},
	}
}

func newAdminMachineDeleteCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var yes bool
	command := &cobra.Command{
		Use:   "delete tenant[/project]/machine",
		Short: "Delete a Sandcastle machine in any tenant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("refusing to delete machine without --yes")
			}
			cfg, userRef, adminRef, err := adminMachineConfig(config, args[0])
			if err != nil {
				return err
			}
			plan, err := sandbox.PlanLifecycle(cmd.Context(), cfg.adminConfig, cfg.projectStore, cfg.sandboxStore, sandbox.LifecycleRequest{
				Reference: userRef,
				Action:    sandbox.ActionRemove,
			})
			if err != nil {
				return err
			}
			plan.Reference = adminRef
			if cfg.sandboxControl == nil {
				return fmt.Errorf("machine lifecycle executor is not configured")
			}
			if err := cfg.sandboxControl.ApplyLifecycle(cmd.Context(), plan); err != nil {
				return err
			}
			if err := refreshTenantDNS(cmd.Context(), cfg, plan.Tenant); err != nil {
				return err
			}
			return writeOutput(cfg.stdout, opts.output, formatLifecyclePlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&yes, "yes", false, "confirm machine deletion")
	return command
}

func adminMachineConfig(config commandConfig, reference string) (commandConfig, string, string, error) {
	ref, err := naming.ParseAdminMachineRef(reference)
	if err != nil {
		return commandConfig{}, "", "", err
	}
	config.adminConfig.Tenant = ref.Tenant
	config.adminConfig.Project = ""
	userRef := ref.Project + "/" + ref.Machine
	return config, userRef, ref.String(), nil
}

func adminMachineListConfig(config commandConfig, reference string) (commandConfig, string, error) {
	if strings.Contains(reference, "/") {
		ref, err := naming.ParseProjectRef(reference)
		if err != nil {
			return commandConfig{}, "", err
		}
		config.adminConfig.Tenant = ref.Tenant
		config.adminConfig.Project = ""
		return config, ref.Project, nil
	}
	ref, err := naming.ParseTenantRef(reference)
	if err != nil {
		return commandConfig{}, "", err
	}
	config.adminConfig.Tenant = ref.Tenant
	config.adminConfig.Project = ""
	return config, "", nil
}
