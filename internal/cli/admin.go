package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

func newAdminCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	admin := &cobra.Command{
		Use:   "admin",
		Short: "Run Sandcastle administrator commands",
	}
	admin.AddCommand(newAdminVersionCommand(config, opts))
	admin.AddCommand(newAdminProjectCommand(config, opts))
	return admin
}

func newAdminVersionCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Sandcastle admin command version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := versionPayload{Name: config.name, Version: version}
			return writeOutput(config.stdout, opts.output, version, payload)
		},
	}
}

func newAdminProjectCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	project := &cobra.Command{
		Use:   "project",
		Short: "Manage Sandcastle projects",
	}
	project.AddCommand(newAdminProjectListCommand(config, opts))
	project.AddCommand(newAdminProjectCreateCommand(config, opts))
	return project
}

func newAdminProjectListCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Sandcastle-managed Incus projects",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			projects, err := listProjects(cmd.Context(), config.projectStore)
			if err != nil {
				return err
			}
			payload := listPayload{Projects: projects}
			return writeOutput(config.stdout, opts.output, formatProjectList(projects), payload)
		},
	}
}

func newAdminProjectCreateCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var domain string
	var dryRun bool
	command := &cobra.Command{
		Use:   "create owner/project",
		Short: "Create a Sandcastle project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var occupiedCIDRs []string
			if !dryRun {
				existingProjects, err := listProjects(cmd.Context(), config.projectStore)
				if err != nil {
					return err
				}
				occupiedCIDRs = project.OccupiedCIDRs(existingProjects)
			}
			plan, err := project.PlanCreate(config.adminConfig, project.CreateRequest{
				Reference:     args[0],
				Domain:        domain,
				OccupiedCIDRs: occupiedCIDRs,
			})
			if err != nil {
				return err
			}
			if !dryRun {
				if config.projectCreator == nil {
					return fmt.Errorf("project creation executor is not configured")
				}
				if err := config.projectCreator.CreateProject(cmd.Context(), plan); err != nil {
					return err
				}
			}
			return writeOutput(config.stdout, opts.output, formatCreatePlan(plan), plan)
		},
	}
	command.Flags().StringVar(&domain, "domain", "", "private project DNS domain")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the Incus creation plan without mutating resources")
	_ = command.MarkFlagRequired("domain")
	return command
}

func formatCreatePlan(plan project.CreatePlan) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Project: %s\n", plan.Reference)
	fmt.Fprintf(&builder, "Incus project: %s\n", plan.IncusProject)
	fmt.Fprintf(&builder, "Domain: %s\n", plan.Domain)
	fmt.Fprintf(&builder, "Private CIDR: %s\n", plan.PrivateCIDR)
	fmt.Fprintf(&builder, "Network: %s\n", plan.PrivateNetwork)
	fmt.Fprintf(&builder, "Volumes: %s, %s, %s\n", plan.HomeVolume, plan.WorkspaceVolume, plan.CAVolume)
	fmt.Fprintf(&builder, "Sidecars: %s (%s), %s (%s)", plan.TailscaleInstance, plan.TailscaleAddress, plan.DNSInstance, plan.DNSAddress)
	return builder.String()
}
