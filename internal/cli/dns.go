package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/dns"
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

func dnsProject(summary project.Summary) dns.Project {
	return dns.Project{
		IncusName:   summary.IncusName,
		Owner:       summary.Owner,
		Name:        summary.Name,
		Domain:      summary.Domain,
		PrivateCIDR: summary.PrivateCIDR,
	}
}
