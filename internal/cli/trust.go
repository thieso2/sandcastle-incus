package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/localtrust"
)

func newTrustCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "trust",
		Short: "Manage local project CA trust",
	}
	command.AddCommand(newTrustInstallCommand(config, opts))
	command.AddCommand(newTrustUninstallCommand(config, opts))
	return command
}

func newTrustInstallCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "install project",
		Short: "Install a project CA into local trust",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := localtrust.PlanInstall(cmd.Context(), config.adminConfig, config.projectStore, localtrust.Request{Reference: args[0]})
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatTrustPlan("Install", plan), plan)
			}
			if config.localTrust == nil {
				return fmt.Errorf("local trust executor is not configured")
			}
			result, err := config.localTrust.Install(cmd.Context(), plan)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatTrustResult(result), result)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the trust install plan without changing local trust")
	return command
}

func newTrustUninstallCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "uninstall project",
		Short: "Remove a project CA from local trust",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := localtrust.PlanUninstall(cmd.Context(), config.adminConfig, config.projectStore, localtrust.Request{Reference: args[0]})
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatTrustPlan("Uninstall", plan), plan)
			}
			if config.localTrust == nil {
				return fmt.Errorf("local trust executor is not configured")
			}
			result, err := config.localTrust.Uninstall(cmd.Context(), plan)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatTrustResult(result), result)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the trust uninstall plan without changing local trust")
	return command
}

func formatTrustPlan(action string, plan localtrust.Plan) string {
	return fmt.Sprintf("%s project CA trust: %s\nCA: %s:%s%s\nWarning: %s", action, plan.Reference, plan.IncusProject, plan.CAVolume, plan.CertificatePath, plan.Warning)
}

func formatTrustResult(result localtrust.Result) string {
	if result.Target == "" {
		return fmt.Sprintf("%s project CA trust: %s", result.Action, result.Reference)
	}
	return fmt.Sprintf("%s project CA trust: %s\nTarget: %s", result.Action, result.Reference, result.Target)
}
