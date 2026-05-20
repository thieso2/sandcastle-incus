package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/tailscale"
)

func newTailscaleCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "tailscale",
		Short: "Manage project Tailscale attachment",
	}
	command.AddCommand(newTailscaleUpCommand(config, opts))
	return command
}

func newTailscaleUpCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	var authKey string
	var advertiseTags []string
	command := &cobra.Command{
		Use:   "up owner/project",
		Short: "Attach a project Tailscale sidecar",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := tailscale.PlanUp(cmd.Context(), config.adminConfig, config.projectStore, tailscale.UpRequest{
				Reference:     args[0],
				AuthKey:       authKey,
				AdvertiseTags: advertiseTags,
			})
			if err != nil {
				return err
			}
			if !dryRun {
				if config.tailscale == nil {
					return fmt.Errorf("tailscale executor is not configured")
				}
				if err := config.tailscale.RunUp(cmd.Context(), plan, tailscale.RunSession{
					Stdin:  config.stdin,
					Stdout: config.stdout,
					Stderr: config.stderr,
				}); err != nil {
					return err
				}
			}
			return writeOutput(config.stdout, opts.output, formatTailscaleUp(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the Tailscale up plan without running tailscale")
	command.Flags().StringVar(&authKey, "auth-key", "", "Tailscale auth key for unattended attachment")
	command.Flags().StringSliceVar(&advertiseTags, "advertise-tag", defaultAdvertiseTags(), "Tailscale tags to advertise")
	return command
}

func formatTailscaleUp(plan tailscale.UpPlan) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Tailscale: %s\n", plan.Reference)
	fmt.Fprintf(&builder, "Sidecar: %s\n", plan.InstanceName)
	fmt.Fprintf(&builder, "Advertise routes: %s", strings.Join(plan.AdvertiseRoutes, ","))
	if len(plan.AdvertiseTags) > 0 {
		fmt.Fprintf(&builder, "\nAdvertise tags: %s", strings.Join(plan.AdvertiseTags, ","))
	}
	if plan.HasAuthKey {
		fmt.Fprint(&builder, "\nAuth key: <redacted>")
	}
	return builder.String()
}

func defaultAdvertiseTags() []string {
	value := strings.TrimSpace(os.Getenv("SANDCASTLE_E2E_TAILSCALE_TAG"))
	if value == "" {
		value = tailscale.DefaultAdvertiseTag
	}
	return []string{value}
}
