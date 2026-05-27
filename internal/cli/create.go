package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
)

func newCreateCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	var detach bool
	var template string
	var appPort int
	var homeDir string
	var workspaceDir string
	var shareHome bool
	var containerTools bool
	var cloudIdentity string
	var authHostname string
	var maxPolls int
	var debugApprove bool
	command := &cobra.Command{
		Use:   "create [project:]machine",
		Short: "Create a Sandcastle container machine",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			createTenantStore := tenantStoreWithSSHKeyMetadata(config.tenantStore)
			plan, err := machine.PlanCreate(cmd.Context(), config.adminConfig, createTenantStore, config.machineStore, machine.CreateRequest{
				Reference:      args[0],
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
			if !dryRun {
				if err := ensureTenantUnixUserForMachineCreate(cmd.Context(), config); err != nil {
					return err
				}
				plan, err = machine.PlanCreate(cmd.Context(), config.adminConfig, createTenantStore, config.machineStore, machine.CreateRequest{
					Reference:      args[0],
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
				if config.machineCreator == nil {
					return fmt.Errorf("machine creation executor is not configured")
				}
				if strings.TrimSpace(cloudIdentity) == "" {
					verboseCLI(config, "workload identity: not requested for create %s; gcloud credentials will not be configured (use --cloud-identity gcp)", plan.Reference)
				}
				if err := config.machineCreator.CreateMachine(cmd.Context(), plan); err != nil {
					return err
				}
				if strings.TrimSpace(cloudIdentity) != "" {
					result, err := enableWorkloadIdentityForPlan(cmd.Context(), config, plan, workloadEnableOptions{
						AuthHostname:  authHostname,
						CloudIdentity: cloudIdentity,
						MaxPolls:      maxPolls,
						DebugApprove:  debugApprove,
					})
					if err != nil {
						return err
					}
					if err := applyWorkloadIdentityToMachine(cmd.Context(), config, plan, result); err != nil {
						return err
					}
				}
				if err := refreshTenantDNS(cmd.Context(), config, plan.Tenant); err != nil {
					return err
				}
				if err := refreshMachineKnownHosts(cmd.Context(), config, plan); err != nil {
					return err
				}
				if !detach {
					if config.machineConnector == nil {
						return fmt.Errorf("machine connect executor is not configured")
					}
					connectPlan, err := machine.PlanConnect(cmd.Context(), config.adminConfig, config.tenantStore, config.machineStore, machine.ConnectRequest{Reference: args[0]})
					if err != nil {
						return err
					}
					if err := config.machineConnector.ConnectMachine(cmd.Context(), connectPlan, machine.ConnectSession{
						Stdin:  config.stdin,
						Stdout: config.stdout,
						Stderr: config.stderr,
					}); err != nil {
						return err
					}
				}
			}
			return writeOutput(config.stdout, opts.output, formatMachinePlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the machine creation plan without creating a container")
	command.Flags().BoolVar(&detach, "detach", false, "create the machine without connecting to it")
	command.Flags().BoolVar(&detach, "background", false, "create the machine without connecting to it")
	command.Flags().StringVar(&template, "template", "", "machine template to use (ai or base)")
	command.Flags().IntVar(&appPort, "app-port", 0, "application port proxied by machine Caddy")
	command.Flags().StringVar(&homeDir, "home-dir", "", "project home volume subdirectory")
	command.Flags().StringVar(&workspaceDir, "workspace-dir", "", "project workspace volume subdirectory")
	command.Flags().BoolVar(&shareHome, "share-home", false, "deprecated no-op; project home storage is shared by default")
	command.Flags().BoolVar(&containerTools, "container-tools", false, "enable nested container tooling for this machine")
	command.Flags().StringVar(&cloudIdentity, "cloud-identity", "", "Cloud Identity Config name to inject, for example gcp")
	command.Flags().StringVar(&authHostname, "auth-hostname", "", "public Auth Hostname (overrides config auth.hostname)")
	command.Flags().IntVar(&maxPolls, "max-polls", 300, "maximum device login poll attempts when enabling workload identity")
	command.Flags().BoolVar(&debugApprove, "debug-approve", false, "auto-approve workload identity device login (requires server --debug-device-user)")
	return command
}

func formatMachinePlan(plan machine.CreatePlan) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Machine: %s\n", plan.Reference)
	fmt.Fprintf(&builder, "Instance: %s\n", plan.InstanceName)
	fmt.Fprintf(&builder, "Private IP: %s\n", plan.PrivateIP)
	fmt.Fprintf(&builder, "App port: %d\n", plan.AppPort)
	fmt.Fprintf(&builder, "Linux user: %s\n", plan.LinuxUser)
	fmt.Fprintf(&builder, "Template: %s\n", plan.Template)
	fmt.Fprintf(&builder, "Home dir: %s\n", plan.HomeDir)
	fmt.Fprintf(&builder, "Workspace dir: %s\n", plan.WorkspaceDir)
	fmt.Fprintf(&builder, "Container tools: %s\n", enabledString(plan.ContainerTools))
	fmt.Fprintf(&builder, "Image: %s", plan.ImageAlias)
	return builder.String()
}

func enabledString(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}
