package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/authapp"
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
	command.AddCommand(newProjectCreateV2Command(config, opts))
	command.AddCommand(newProjectStatusCommand(config, opts))
	command.AddCommand(newProjectSetCloudIdentityCommand(config, opts))
	command.AddCommand(newProjectUnsetCloudIdentityCommand(config, opts))
	command.AddCommand(newProjectSetDockerAutostartCommand(config, opts))
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

func newProjectSetCloudIdentityCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "set-cloud-identity name cloud-identity",
		Short: "Set a default Cloud Identity Config for a project",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := tenant.PlanSetProjectCloudIdentity(cmd.Context(), config.adminConfig, config.tenantStore, tenant.ProjectMutationRequest{
				Name:          args[0],
				CloudIdentity: strings.TrimSpace(args[1]),
			})
			if err != nil {
				return err
			}
			if err := validateProjectCloudIdentity(cmd.Context(), config, plan.Tenant.Tenant, strings.TrimSpace(args[1])); err != nil {
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

func validateProjectCloudIdentity(ctx context.Context, config commandConfig, tenantName string, cloudIdentity string) error {
	tenantName = strings.TrimSpace(tenantName)
	cloudIdentity = strings.TrimSpace(cloudIdentity)
	if tenantName == "" || cloudIdentity == "" {
		return fmt.Errorf("tenant and cloud identity config are required")
	}
	client := config.authCloudIdentity
	if client == nil {
		baseURL := commandAuthHostname(config, "")
		if baseURL == "" {
			return fmt.Errorf("cannot validate cloud identity %q for tenant %q: --auth-hostname is required (or run sc login again)", cloudIdentity, tenantName)
		}
		if strings.TrimSpace(config.adminConfig.AuthToken) == "" {
			return fmt.Errorf("cannot validate cloud identity %q for tenant %q: run sc login first", cloudIdentity, tenantName)
		}
		client = authapp.DeviceClient{BaseURL: baseURL, AuthToken: config.adminConfig.AuthToken}
	}
	configured, err := client.GetCloudIdentity(ctx, tenantName, cloudIdentity)
	if err != nil {
		return fmt.Errorf("cloud identity config %q is not configured for tenant %q: %w", cloudIdentity, tenantName, err)
	}
	if strings.TrimSpace(configured.Tenant) != tenantName {
		return fmt.Errorf("cloud identity config %q belongs to tenant %q, not tenant %q", cloudIdentity, configured.Tenant, tenantName)
	}
	return nil
}

func newProjectUnsetCloudIdentityCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "unset-cloud-identity name",
		Short: "Clear the default Cloud Identity Config for a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := tenant.PlanSetProjectCloudIdentity(cmd.Context(), config.adminConfig, config.tenantStore, tenant.ProjectMutationRequest{Name: args[0]})
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

func newProjectSetDockerAutostartCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "set-docker-autostart name on|off",
		Short: "Set whether Docker starts automatically for new machines in a project",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			enabled, err := parseOnOff(args[1])
			if err != nil {
				return err
			}
			plan, err := tenant.PlanSetProjectDockerAutostart(cmd.Context(), config.adminConfig, config.tenantStore, tenant.ProjectMutationRequest{
				Name:            args[0],
				DockerAutostart: enabled,
			})
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

func parseOnOff(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "yes", "1", "enabled", "enable":
		return true, nil
	case "off", "false", "no", "0", "disabled", "disable":
		return false, nil
	default:
		return false, fmt.Errorf("value must be on or off")
	}
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
	var builder strings.Builder
	fmt.Fprintf(&builder, "Project: %s\n", status.Project.Name)
	fmt.Fprintf(&builder, "Tenant: %s\n", status.Tenant.Tenant)
	if status.Project.CloudIdentity != "" {
		fmt.Fprintf(&builder, "Cloud identity: %s\n", status.Project.CloudIdentity)
	}
	if status.Project.DockerAutostart {
		fmt.Fprintln(&builder, "Docker autostart: on")
	}
	fmt.Fprintf(&builder, "Machines: %d", status.MachineCount)
	return builder.String()
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
