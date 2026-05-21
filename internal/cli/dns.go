package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	"github.com/thieso2/sandcastle-incus/internal/localdns"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func newDNSCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "dns",
		Short: "Manage project DNS",
	}
	command.AddCommand(newDNSApplyCommand(config, opts))
	command.AddCommand(newDNSStatusCommand(config, opts))
	command.AddCommand(newDNSInstallCommand(config, opts))
	command.AddCommand(newDNSRefreshCommand(config, opts))
	command.AddCommand(newDNSUninstallCommand(config, opts))
	command.AddCommand(newDNSForwarderCommand())
	command.AddCommand(newDNSServiceCommand(config, opts))
	return command
}

func newDNSApplyCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "apply project",
		Short: "Render and apply project CoreDNS records",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			summary, err := findProjectSummary(cmd.Context(), config.projectStore, args[0], config.adminConfig.Owner)
			if err != nil {
				return err
			}
			if config.dnsApplier == nil {
				return fmt.Errorf("DNS apply executor is not configured")
			}
			result, err := config.dnsApplier.Apply(cmd.Context(), dnsProject(summary))
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatDNSApply(result), result)
		},
	}
}

func newDNSStatusCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status project",
		Short: "Render project DNS status without applying it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			summary, err := findProjectSummary(cmd.Context(), config.projectStore, args[0], config.adminConfig.Owner)
			if err != nil {
				return err
			}
			result, err := dns.PlanApply(dnsProject(summary), nil)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatDNSApply(result), result)
		},
	}
}

func newDNSInstallCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "install project",
		Short: "Install local resolver state for a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := localdns.PlanInstall(cmd.Context(), config.adminConfig, config.projectStore, localdns.Request{Reference: args[0]})
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatLocalDNSPlan("Install", plan), plan)
			}
			if config.localDNS == nil {
				return fmt.Errorf("local DNS executor is not configured")
			}
			result, err := config.localDNS.Install(cmd.Context(), plan)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatLocalDNSResult(result), result)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the local DNS install plan without changing local resolver state")
	return command
}

func newDNSRefreshCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "refresh project",
		Short: "Refresh local resolver state for a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := localdns.PlanRefresh(cmd.Context(), config.adminConfig, config.projectStore, localdns.Request{Reference: args[0]})
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatLocalDNSPlan("Refresh", plan), plan)
			}
			if config.localDNS == nil {
				return fmt.Errorf("local DNS executor is not configured")
			}
			result, err := config.localDNS.Refresh(cmd.Context(), plan)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatLocalDNSResult(result), result)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the local DNS refresh plan without changing local resolver state")
	return command
}

func newDNSUninstallCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "uninstall project",
		Short: "Remove local resolver state for a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := localdns.PlanUninstall(cmd.Context(), config.adminConfig, config.projectStore, localdns.Request{Reference: args[0]})
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatLocalDNSPlan("Uninstall", plan), plan)
			}
			if config.localDNS == nil {
				return fmt.Errorf("local DNS executor is not configured")
			}
			result, err := config.localDNS.Uninstall(cmd.Context(), plan)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatLocalDNSResult(result), result)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the local DNS uninstall plan without changing local resolver state")
	return command
}

func newDNSForwarderCommand() *cobra.Command {
	var statePath string
	var listen string
	command := &cobra.Command{
		Use:   "forwarder",
		Short: "Run the local DNS forwarder",
		RunE: func(cmd *cobra.Command, args []string) error {
			return localdns.Forwarder{
				StatePath: statePath,
				Listen:    listen,
			}.Serve(cmd.Context())
		},
	}
	command.Flags().StringVar(&statePath, "state", localdns.DefaultStatePath(), "local DNS state file")
	command.Flags().StringVar(&listen, "listen", "127.0.0.1:53541", "local UDP listen address")
	return command
}

func newDNSServiceCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "service",
		Short: "Manage the local DNS forwarder service",
	}
	command.AddCommand(newDNSServiceInstallCommand(config, opts))
	command.AddCommand(newDNSServiceReloadCommand(config, opts))
	command.AddCommand(newDNSServiceUninstallCommand(config, opts))
	return command
}

func newDNSServiceInstallCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "install",
		Short: "Install and start the local DNS forwarder service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := localdns.PlanServiceInstall()
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatLocalDNSServicePlan(plan), plan)
			}
			if config.localDNSService == nil {
				return fmt.Errorf("local DNS service executor is not configured")
			}
			result, err := config.localDNSService.InstallService(cmd.Context(), plan)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatLocalDNSServiceResult(result), result)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the local DNS service install plan without changing service state")
	return command
}

func newDNSServiceReloadCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "reload",
		Short: "Reload the local DNS forwarder service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := localdns.PlanServiceReload()
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatLocalDNSServicePlan(plan), plan)
			}
			if config.localDNSService == nil {
				return fmt.Errorf("local DNS service executor is not configured")
			}
			result, err := config.localDNSService.ReloadService(cmd.Context(), plan)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatLocalDNSServiceResult(result), result)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the local DNS service reload plan without changing service state")
	return command
}

func newDNSServiceUninstallCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the local DNS forwarder service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := localdns.PlanServiceUninstall()
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatLocalDNSServicePlan(plan), plan)
			}
			if config.localDNSService == nil {
				return fmt.Errorf("local DNS service executor is not configured")
			}
			result, err := config.localDNSService.UninstallService(cmd.Context(), plan)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatLocalDNSServiceResult(result), result)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the local DNS service uninstall plan without changing service state")
	return command
}

func findProjectSummary(ctx context.Context, store project.IncusProjectStore, reference string, defaultOwner string) (project.Summary, error) {
	ref, err := naming.ParseProjectRefWithDefaultOwner(reference, defaultOwner)
	if err != nil {
		return project.Summary{}, err
	}
	projects, err := project.List(ctx, store)
	if err != nil {
		return project.Summary{}, err
	}
	for _, summary := range projects {
		if summary.Owner == ref.Owner && summary.Name == ref.Project {
			return summary, nil
		}
	}
	return project.Summary{}, fmt.Errorf("Sandcastle project %s not found", ref.String())
}

func formatDNSApply(result dns.ApplyResult) string {
	return fmt.Sprintf("DNS records for %s/%s: %d", result.Project.Owner, result.Project.Name, result.RecordCount)
}

func formatLocalDNSPlan(action string, plan localdns.Plan) string {
	output := fmt.Sprintf("%s local DNS: %s\nDomain: %s\nForwarder: %s\nProject DNS: %s\nResolver: %s", action, plan.Reference, plan.Domain, plan.Listen, plan.DNSEndpoint, plan.ResolverStrategy)
	if len(plan.ResolverCommands) > 0 {
		output += "\nResolver commands:"
		for _, command := range plan.ResolverCommands {
			output += "\n  " + strings.Join(command.Args, " ")
		}
	}
	return output
}

func formatLocalDNSResult(result localdns.Result) string {
	return fmt.Sprintf("%s local DNS: %s\nState: %s\nResolver: %s", result.Action, result.Reference, result.StatePath, result.ResolverPath)
}

func formatLocalDNSServicePlan(plan localdns.ServicePlan) string {
	return fmt.Sprintf("%s local DNS service\nStrategy: %s\nService: %s\nForwarder: %s", plan.Action, plan.Strategy, plan.ServicePath, plan.Listen)
}

func formatLocalDNSServiceResult(result localdns.ServiceResult) string {
	return fmt.Sprintf("%s local DNS service\nStrategy: %s\nService: %s", result.Action, result.Strategy, result.ServicePath)
}

func dnsProject(summary project.Summary) dns.Project {
	return dns.Project{
		IncusName:   summary.IncusName,
		Owner:       summary.Owner,
		Name:        summary.Name,
		Domain:      summary.Domain,
		PrivateCIDR: summary.PrivateCIDR,
	}
}
