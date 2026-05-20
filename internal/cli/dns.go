package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	"github.com/thieso2/sandcastle-incus/internal/localdns"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/project"
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
	return command
}

func newDNSApplyCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "apply owner/project",
		Short: "Render and apply project CoreDNS records",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			summary, err := findProjectSummary(cmd.Context(), config.projectStore, args[0])
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
		Use:   "status owner/project",
		Short: "Render project DNS status without applying it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			summary, err := findProjectSummary(cmd.Context(), config.projectStore, args[0])
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
		Use:   "install owner/project",
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
		Use:   "refresh owner/project",
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
		Use:   "uninstall owner/project",
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

func findProjectSummary(ctx context.Context, store project.IncusProjectStore, reference string) (project.Summary, error) {
	ref, err := naming.ParseProjectRef(reference)
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
	return fmt.Sprintf("%s local DNS: %s\nDomain: %s\nForwarder: %s\nProject DNS: %s\nResolver: %s", action, plan.Reference, plan.Domain, plan.Listen, plan.DNSEndpoint, plan.ResolverStrategy)
}

func formatLocalDNSResult(result localdns.Result) string {
	return fmt.Sprintf("%s local DNS: %s\nState: %s\nResolver: %s", result.Action, result.Reference, result.StatePath, result.ResolverPath)
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
