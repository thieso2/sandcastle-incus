package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thieso2/sandcastle-incus/internal/images"
)

func newImageCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	imageCommand := &cobra.Command{
		Use:   "image",
		Short: "Work with Sandcastle Images for your tenant",
	}
	imageCommand.AddCommand(newImagePullCommand(config, opts))
	return imageCommand
}

func newImagePullCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "pull [base|ai]",
		Short: "Pull the deployment's newest Sandcastle Images into your tenant",
		Long: "Refresh your tenant's base and AI image aliases from the deployment's " +
			"published images so that new machines use the latest build. Existing " +
			"machines keep their current image until recreated.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var templates []string
			if len(args) == 1 {
				template := strings.ToLower(strings.TrimSpace(args[0]))
				if template != "base" && template != "ai" {
					return fmt.Errorf("unknown image template %q (want base or ai)", args[0])
				}
				templates = []string{template}
			}

			summary, err := findTenantSummary(cmd.Context(), config.tenantStore, config.adminConfig.Tenant)
			if err != nil {
				return err
			}

			plan, err := images.PlanPull(config.adminConfig, images.PullRequest{
				Remote:        config.adminConfig.Remote,
				TenantProject: summary.IncusName,
				Templates:     templates,
			})
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatImagePullPlan(plan), plan)
			}
			if config.imagePuller == nil {
				return fmt.Errorf("image puller is not configured")
			}
			result, err := config.imagePuller.PullImages(cmd.Context(), plan)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatImagePullResult(result), result)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the image pull plan without changing aliases")
	return command
}

func formatImagePullPlan(plan images.PullPlan) string {
	return fmt.Sprintf("Pull %s into %s (from %s project)",
		strings.Join(plan.Aliases, ", "), plan.TenantProject, plan.SourceProject)
}

func formatImagePullResult(result images.PullResult) string {
	if len(result.Pulled) == 0 {
		return "No images pulled"
	}
	return fmt.Sprintf("Pulled %s into %s", strings.Join(result.Pulled, ", "), result.TenantProject)
}
