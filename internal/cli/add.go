package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
)

func newAddCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	var detach bool
	var template string
	var appPort int
	var homeDir string
	var workspaceDir string
	var shareHome bool
	var containerTools bool
	command := &cobra.Command{
		Use:   "add project/name",
		Short: "Create a Sandcastle container sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := sandbox.PlanCreate(cmd.Context(), config.adminConfig, config.projectStore, config.sandboxStore, sandbox.CreateRequest{
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
				if config.sandboxCreator == nil {
					return fmt.Errorf("sandbox creation executor is not configured")
				}
				if err := config.sandboxCreator.CreateSandbox(cmd.Context(), plan); err != nil {
					return err
				}
				if !detach {
					if config.sandboxEnterer == nil {
						return fmt.Errorf("sandbox enter executor is not configured")
					}
					enterPlan, err := sandbox.PlanEnter(cmd.Context(), config.adminConfig, config.projectStore, sandbox.EnterRequest{Reference: args[0]})
					if err != nil {
						return err
					}
					if err := config.sandboxEnterer.EnterSandbox(cmd.Context(), enterPlan, sandbox.EnterSession{
						Stdin:  config.stdin,
						Stdout: config.stdout,
						Stderr: config.stderr,
					}); err != nil {
						return err
					}
				}
			}
			return writeOutput(config.stdout, opts.output, formatSandboxPlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the sandbox creation plan without creating a container")
	command.Flags().BoolVar(&detach, "detach", false, "create the sandbox without entering it")
	command.Flags().BoolVar(&detach, "background", false, "create the sandbox without entering it")
	command.Flags().StringVar(&template, "template", "", "sandbox template to use (ai or base)")
	command.Flags().IntVar(&appPort, "app-port", 0, "application port proxied by sandbox Caddy")
	command.Flags().StringVar(&homeDir, "home-dir", "", "project home volume subdirectory")
	command.Flags().StringVar(&workspaceDir, "workspace-dir", "", "project workspace volume subdirectory")
	command.Flags().BoolVar(&shareHome, "share-home", false, "confirm sharing a home subdirectory with another running sandbox")
	command.Flags().BoolVar(&containerTools, "container-tools", false, "enable nested container tooling for this sandbox")
	return command
}

func formatSandboxPlan(plan sandbox.CreatePlan) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Sandbox: %s\n", plan.Reference)
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
