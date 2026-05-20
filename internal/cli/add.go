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
	var appPort int
	var homeDir string
	var workspaceDir string
	command := &cobra.Command{
		Use:   "add owner/project/name",
		Short: "Create a Sandcastle container sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := sandbox.PlanCreate(cmd.Context(), config.adminConfig, config.projectStore, config.sandboxStore, sandbox.CreateRequest{
				Reference:    args[0],
				AppPort:      appPort,
				HomeDir:      homeDir,
				WorkspaceDir: workspaceDir,
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
	command.Flags().IntVar(&appPort, "app-port", 0, "application port proxied by sandbox Caddy")
	command.Flags().StringVar(&homeDir, "home-dir", "", "project home volume subdirectory")
	command.Flags().StringVar(&workspaceDir, "workspace-dir", "", "project workspace volume subdirectory")
	return command
}

func formatSandboxPlan(plan sandbox.CreatePlan) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Sandbox: %s\n", plan.Reference)
	fmt.Fprintf(&builder, "Instance: %s\n", plan.InstanceName)
	fmt.Fprintf(&builder, "Private IP: %s\n", plan.PrivateIP)
	fmt.Fprintf(&builder, "App port: %d\n", plan.AppPort)
	fmt.Fprintf(&builder, "Image: %s", plan.ImageAlias)
	return builder.String()
}
