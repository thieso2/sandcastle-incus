package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/infra"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/routebroker"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

func newAdminCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	admin := &cobra.Command{
		Use:   "admin",
		Short: "Run Sandcastle administrator commands",
	}
	admin.AddCommand(newAdminVersionCommand(config, opts))
	admin.AddCommand(newAdminProjectCommand(config, opts))
	admin.AddCommand(newAdminUserCommand(config, opts))
	admin.AddCommand(newAdminInfraCommand(config, opts))
	admin.AddCommand(newAdminRouteBrokerCommand(config))
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
	project.AddCommand(newAdminProjectStatusCommand(config, opts))
	project.AddCommand(newAdminProjectCreateCommand(config, opts))
	project.AddCommand(newAdminProjectDeleteCommand(config, opts))
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

func newAdminProjectStatusCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status owner/project",
		Short: "Show Sandcastle-managed Incus project status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			status, err := project.GetStatusWithTopology(
				cmd.Context(),
				config.projectStore,
				config.topologyStore,
				project.TopologyRequest{StoragePool: config.adminConfig.StoragePool},
				args[0],
			)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatProjectStatus(status), status)
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

func newAdminProjectDeleteCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var yes bool
	var purge bool
	command := &cobra.Command{
		Use:   "delete owner/project",
		Short: "Delete a Sandcastle project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("refusing to delete without --yes")
			}
			plan, err := project.PlanDelete(config.adminConfig, project.DeleteRequest{
				Reference: args[0],
				Purge:     purge,
			})
			if err != nil {
				return err
			}
			if config.projectDeleter == nil {
				return fmt.Errorf("project deletion executor is not configured")
			}
			if err := config.projectDeleter.DeleteProject(cmd.Context(), plan); err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatDeletePlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&yes, "yes", false, "confirm project deletion")
	command.Flags().BoolVar(&purge, "purge", false, "delete durable project volumes and the Incus project")
	return command
}

func formatDeletePlan(plan project.DeletePlan) string {
	if plan.PurgeDurableState {
		return fmt.Sprintf("Deleted %s and purged durable state.", plan.Reference)
	}
	return fmt.Sprintf("Deleted runtime resources for %s; durable state was preserved.", plan.Reference)
}

func newAdminInfraCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	infraCommand := &cobra.Command{
		Use:   "infra",
		Short: "Manage Sandcastle shared infrastructure",
	}
	infraCommand.AddCommand(newAdminInfraCreateCommand(config, opts))
	return infraCommand
}

func newAdminInfraCreateCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "create",
		Short: "Create Sandcastle shared infrastructure",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := infra.PlanCreate(config.adminConfig, infra.CreateRequest{})
			if err != nil {
				return err
			}
			if !dryRun {
				if config.infraCreator == nil {
					return fmt.Errorf("infrastructure creation executor is not configured")
				}
				if err := config.infraCreator.CreateInfrastructure(cmd.Context(), plan); err != nil {
					return err
				}
			}
			return writeOutput(config.stdout, opts.output, formatInfraCreatePlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the infrastructure creation plan without mutating resources")
	return command
}

func formatInfraCreatePlan(plan infra.CreatePlan) string {
	return fmt.Sprintf("Infrastructure project: %s\nRuntime: %s, %s", plan.Project, plan.CaddyInstance, plan.RouteBrokerInstance)
}

func newAdminUserCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	user := &cobra.Command{
		Use:   "user",
		Short: "Manage Sandcastle restricted users",
	}
	user.AddCommand(newAdminUserCreateCommand(config, opts))
	user.AddCommand(newAdminUserGrantCommand(config, opts))
	user.AddCommand(newAdminUserTokenCommand(config, opts))
	return user
}

func newAdminUserCreateCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "create user",
		Short: "Create a restricted Sandcastle user certificate token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := usertrust.PlanCreateUser(args[0])
			if err != nil {
				return err
			}
			if !dryRun {
				if config.trustManager == nil {
					return fmt.Errorf("restricted user certificate executor is not configured")
				}
				result, err := config.trustManager.CreateToken(cmd.Context(), plan)
				if err != nil {
					return err
				}
				return writeOutput(config.stdout, opts.output, formatTokenResult(result), result)
			}
			return writeOutput(config.stdout, opts.output, formatUserPlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the restricted user plan without mutating trust state")
	return command
}

func newAdminUserGrantCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "grant user owner/project [owner/project...]",
		Short: "Plan restricted certificate project grants",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := usertrust.PlanGrant(config.adminConfig, usertrust.GrantRequest{
				User:     args[0],
				Projects: args[1:],
			})
			if err != nil {
				return err
			}
			if !dryRun {
				if config.trustManager == nil {
					return fmt.Errorf("restricted user grant executor is not configured")
				}
				if err := config.trustManager.Grant(cmd.Context(), plan); err != nil {
					return err
				}
			}
			return writeOutput(config.stdout, opts.output, formatUserPlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the grant plan without mutating trust state")
	return command
}

func newAdminUserTokenCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "token user",
		Short: "Plan a restricted certificate add token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := usertrust.PlanToken(args[0])
			if err != nil {
				return err
			}
			if !dryRun {
				if config.trustManager == nil {
					return fmt.Errorf("restricted user token executor is not configured")
				}
				result, err := config.trustManager.CreateToken(cmd.Context(), plan)
				if err != nil {
					return err
				}
				return writeOutput(config.stdout, opts.output, formatTokenResult(result), result)
			}
			return writeOutput(config.stdout, opts.output, formatUserPlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the token plan without creating a trust token")
	return command
}

func formatUserPlan(plan usertrust.UserPlan) string {
	projects := "none"
	if len(plan.Projects) > 0 {
		projects = strings.Join(plan.Projects, ", ")
	}
	return fmt.Sprintf("User: %s\nCertificate: %s\nRestricted: %t\nProjects: %s", plan.User, plan.CertificateName, plan.Restricted, projects)
}

func formatTokenResult(result usertrust.TokenResult) string {
	return fmt.Sprintf("User: %s\nCertificate: %s\nToken: %s", result.User, result.CertificateName, result.Token)
}

func newAdminRouteBrokerCommand(config commandConfig) *cobra.Command {
	routeBroker := &cobra.Command{
		Use:   "route-broker",
		Short: "Manage the Sandcastle public route broker",
	}
	routeBroker.AddCommand(newAdminRouteBrokerServeCommand(config))
	return routeBroker
}

func newAdminRouteBrokerServeCommand(config commandConfig) *cobra.Command {
	var listen string
	var certFile string
	var keyFile string
	command := &cobra.Command{
		Use:   "serve",
		Short: "Serve the public route broker API over mTLS",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := routebroker.PlanServe(routebroker.ServeRequest{
				Address:  listen,
				CertFile: certFile,
				KeyFile:  keyFile,
			})
			if err != nil {
				return err
			}
			if config.routeBroker == nil {
				return fmt.Errorf("route broker server is not configured")
			}
			return config.routeBroker.Serve(cmd.Context(), plan)
		},
	}
	command.Flags().StringVar(&listen, "listen", ":9443", "route broker listen address")
	command.Flags().StringVar(&certFile, "cert", "", "route broker TLS certificate file")
	command.Flags().StringVar(&keyFile, "key", "", "route broker TLS key file")
	_ = command.MarkFlagRequired("cert")
	_ = command.MarkFlagRequired("key")
	return command
}
