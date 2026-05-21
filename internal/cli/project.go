package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func newProjectCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "project",
		Short: "Manage lightweight projects in the current tenant",
	}
	command.AddCommand(newProjectListCommand(config, opts))
	command.AddCommand(newProjectCreateCommand(config, opts))
	command.AddCommand(newProjectStatusCommand(config, opts))
	command.AddCommand(newProjectDeleteCommand(config, opts))
	return command
}

func newProjectListCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List projects in the current tenant",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tenantSummary, err := currentTenantSummary(cmd.Context(), config)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatProjectNamespaceList(tenantSummary), tenantSummary)
		},
	}
}

func newProjectCreateCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "create name",
		Short: "Create a project namespace in the current tenant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := tenant.PlanCreateProject(cmd.Context(), config.adminConfig, config.tenantStore, tenant.ProjectMutationRequest{Name: args[0]})
			if err != nil {
				return err
			}
			if !dryRun {
				if config.tenantUpdater == nil {
					return fmt.Errorf("project metadata updater is not configured")
				}
				if err := config.tenantUpdater.SetTenantProjects(cmd.Context(), plan.IncusProject, plan.Projects); err != nil {
					return err
				}
			}
			return writeOutput(config.stdout, opts.output, formatProjectMutationPlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the project metadata update without mutating resources")
	return command
}

type projectStatusPayload struct {
	Tenant       tenant.Summary `json:"tenant"`
	Project      meta.Project   `json:"project"`
	MachineCount int            `json:"machineCount"`
}

func newProjectStatusCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status name",
		Short: "Show project status in the current tenant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := naming.ValidateProjectName(args[0]); err != nil {
				return err
			}
			tenantSummary, machines, err := currentTenantMachines(cmd, config)
			if err != nil {
				return err
			}
			project, ok := findProject(tenantSummary, args[0])
			if !ok {
				return fmt.Errorf("Sandcastle project %s not found in tenant %s", args[0], tenantSummary.Tenant)
			}
			count := 0
			for _, machine := range machines {
				if machine.Project == project.Name {
					count++
				}
			}
			payload := projectStatusPayload{
				Tenant:       tenantSummary,
				Project:      project,
				MachineCount: count,
			}
			return writeOutput(config.stdout, opts.output, formatProjectNamespaceStatus(payload), payload)
		},
	}
}

func newProjectDeleteCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var yes bool
	var dryRun bool
	command := &cobra.Command{
		Use:   "delete name",
		Short: "Delete an empty project namespace from the current tenant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("refusing to delete project without --yes")
			}
			tenantSummary, machines, err := currentTenantMachines(cmd, config)
			if err != nil {
				return err
			}
			plan, err := tenant.PlanDeleteProject(cmd.Context(), config.adminConfig, config.tenantStore, tenant.ProjectMutationRequest{
				Name:     args[0],
				Machines: machines,
			})
			if err != nil {
				return err
			}
			plan.Tenant = tenantSummary
			if !dryRun {
				if config.tenantUpdater == nil {
					return fmt.Errorf("project metadata updater is not configured")
				}
				if err := config.tenantUpdater.SetTenantProjects(cmd.Context(), plan.IncusProject, plan.Projects); err != nil {
					return err
				}
			}
			return writeOutput(config.stdout, opts.output, formatProjectMutationPlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&yes, "yes", false, "confirm project deletion")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the project metadata update without mutating resources")
	return command
}

func currentTenantMachines(cmd *cobra.Command, config commandConfig) (tenant.Summary, []meta.Machine, error) {
	result, err := listMachines(cmd.Context(), config, listMachinesRequest{AllProjects: true})
	if err != nil {
		return tenant.Summary{}, nil, err
	}
	return result.Tenant, result.Machines, nil
}

func currentTenantSummary(ctx context.Context, config commandConfig) (tenant.Summary, error) {
	ref, err := naming.ParseTenantRef(config.adminConfig.Tenant)
	if err != nil {
		return tenant.Summary{}, fmt.Errorf("tenant is required; set SANDCASTLE_TENANT or local tenant config")
	}
	tenants, err := listTenants(ctx, config.tenantStore)
	if err != nil {
		return tenant.Summary{}, err
	}
	for _, tenant := range tenants {
		if tenant.Tenant == ref.Tenant {
			return tenant, nil
		}
	}
	return tenant.Summary{}, fmt.Errorf("Sandcastle tenant %s not found", ref.Tenant)
}

func formatProjectNamespaceList(tenant tenant.Summary) string {
	if len(tenant.Projects) == 0 {
		return "No Sandcastle projects found."
	}
	var builder strings.Builder
	for _, namespace := range tenant.Projects {
		fmt.Fprintf(&builder, "%s\n", namespace.Name)
	}
	return strings.TrimRight(builder.String(), "\n")
}

func formatProjectNamespaceStatus(status projectStatusPayload) string {
	return fmt.Sprintf("Project: %s\nTenant: %s\nMachines: %d", status.Project.Name, status.Tenant.Tenant, status.MachineCount)
}

func formatProjectMutationPlan(plan tenant.ProjectMutationPlan) string {
	return fmt.Sprintf("%s project %s in tenant %s", plan.Action, plan.Project.Name, plan.Tenant.Tenant)
}

func findProject(summary tenant.Summary, name string) (meta.Project, bool) {
	for _, project := range summary.Projects {
		if project.Name == name {
			return project, true
		}
	}
	return meta.Project{}, false
}
