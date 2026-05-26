package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	"github.com/thieso2/sandcastle-incus/internal/localdns"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func newDNSCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "dns",
		Short: "Manage tenant DNS",
	}
	command.AddCommand(newDNSApplyCommand(config, opts))
	command.AddCommand(newDNSStatusCommand(config, opts))
	command.AddCommand(newDNSSetupCommand(config, opts))
	command.AddCommand(newDNSTeardownCommand(config, opts))
	command.AddCommand(newDNSInstallCommand(config, opts))
	command.AddCommand(newDNSRefreshCommand(config, opts))
	command.AddCommand(newDNSUninstallCommand(config, opts))
	return command
}

type dnsSetupResult struct {
	Reference string                    `json:"reference"`
	Apply     dns.ApplyResult           `json:"apply"`
	Install   localdns.Result           `json:"install"`
	Refresh   localdns.Result           `json:"refresh"`
	Elevated  []dnsElevatedActionResult `json:"elevated,omitempty"`
}

type dnsTeardownResult struct {
	Reference string                    `json:"reference"`
	Uninstall localdns.Result           `json:"uninstall"`
	Elevated  []dnsElevatedActionResult `json:"elevated,omitempty"`
}

type dnsElevatedActionResult struct {
	Action string `json:"action"`
}

func newDNSSetupCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "setup [tenant]",
		Short: "Apply tenant DNS and install local resolver config",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := runDNSSetup(cmd.Context(), config, optionalArg(args))
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatDNSSetup(result), result)
		},
	}
	return command
}

func runDNSSetup(ctx context.Context, config commandConfig, reference string) (dnsSetupResult, error) {
	installPlan, err := localdns.PlanInstall(ctx, config.adminConfig, config.tenantStore, localdns.Request{Reference: reference})
	if err != nil {
		return dnsSetupResult{}, err
	}
	summary, err := findTenantSummary(ctx, config.tenantStore, installPlan.Reference)
	if err != nil {
		return dnsSetupResult{}, err
	}
	if config.dnsApplier == nil {
		return dnsSetupResult{}, fmt.Errorf("DNS apply executor is not configured")
	}
	applyResult, err := config.dnsApplier.Apply(ctx, dnsProject(summary))
	if err != nil {
		return dnsSetupResult{}, err
	}
	if config.localDNS == nil {
		return dnsSetupResult{}, fmt.Errorf("local DNS executor is not configured")
	}
	installResult, installElevated, err := runLocalDNSWithSudoFallback(ctx, config, "install", installPlan)
	if err != nil {
		return dnsSetupResult{}, err
	}
	refreshPlan, err := localdns.PlanRefresh(ctx, config.adminConfig, config.tenantStore, localdns.Request{Reference: installPlan.Reference})
	if err != nil {
		return dnsSetupResult{}, err
	}
	refreshResult, refreshElevated, err := runLocalDNSWithSudoFallback(ctx, config, "refresh", refreshPlan)
	if err != nil {
		return dnsSetupResult{}, err
	}
	return dnsSetupResult{
		Reference: installPlan.Reference,
		Apply:     applyResult,
		Install:   installResult,
		Refresh:   refreshResult,
		Elevated:  elevatedActions([]string{"install", "refresh"}, installElevated, refreshElevated),
	}, nil
}

func newDNSTeardownCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "teardown [tenant]",
		Short: "Remove local resolver config for a tenant",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reference := optionalArg(args)
			uninstallPlan, err := localdns.PlanUninstall(cmd.Context(), config.adminConfig, config.tenantStore, localdns.Request{Reference: reference})
			if err != nil {
				return err
			}
			if config.localDNS == nil {
				return fmt.Errorf("local DNS executor is not configured")
			}
			uninstallResult, uninstallElevated, err := runLocalDNSWithSudoFallback(cmd.Context(), config, "uninstall", uninstallPlan)
			if err != nil {
				return err
			}
			result := dnsTeardownResult{
				Reference: uninstallPlan.Reference,
				Uninstall: uninstallResult,
				Elevated:  elevatedActions([]string{"uninstall"}, uninstallElevated),
			}
			return writeOutput(config.stdout, opts.output, formatDNSTeardown(result), result)
		},
	}
	return command
}

func newDNSApplyCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "apply tenant",
		Short: "Render and apply tenant CoreDNS records",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			summary, err := findTenantSummary(cmd.Context(), config.tenantStore, args[0])
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
		Use:   "status tenant",
		Short: "Render tenant DNS status without applying it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			summary, err := findTenantSummary(cmd.Context(), config.tenantStore, args[0])
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
		Use:   "install [tenant]",
		Short: "Install local resolver state for a tenant",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := localdns.PlanInstall(cmd.Context(), config.adminConfig, config.tenantStore, localdns.Request{Reference: optionalReference(args)})
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
		Use:   "refresh [tenant]",
		Short: "Refresh local resolver state for a tenant",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := localdns.PlanRefresh(cmd.Context(), config.adminConfig, config.tenantStore, localdns.Request{Reference: optionalReference(args)})
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
		Use:   "uninstall [tenant]",
		Short: "Remove local resolver state for a tenant",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := localdns.PlanUninstall(cmd.Context(), config.adminConfig, config.tenantStore, localdns.Request{Reference: optionalReference(args)})
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

func findTenantSummary(ctx context.Context, store tenant.IncusTenantStore, reference string) (tenant.Summary, error) {
	ref, err := naming.ParseTenantRef(reference)
	if err != nil {
		return tenant.Summary{}, err
	}
	tenants, err := tenant.List(ctx, store)
	if err != nil {
		return tenant.Summary{}, err
	}
	for _, summary := range tenants {
		if summary.Tenant == ref.Tenant {
			return summary, nil
		}
	}
	return tenant.Summary{}, fmt.Errorf("Sandcastle tenant %s not found", ref.String())
}

func formatDNSApply(result dns.ApplyResult) string {
	return fmt.Sprintf("DNS records for %s: %d", result.Tenant.Tenant, result.RecordCount)
}

func formatLocalDNSPlan(action string, plan localdns.Plan) string {
	output := fmt.Sprintf("%s local DNS: %s\nDNS suffix: %s\nTenant DNS: %s\nResolver: %s", action, plan.Reference, plan.DNSSuffix, plan.DNSEndpoint, plan.ResolverStrategy)
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

func formatDNSSetup(result dnsSetupResult) string {
	output := fmt.Sprintf("DNS setup: %s\nDNS records: %d\nResolver: %s\nState: %s", result.Reference, result.Apply.RecordCount, result.Install.ResolverPath, result.Install.StatePath)
	if len(result.Elevated) > 0 {
		actions := make([]string, 0, len(result.Elevated))
		for _, action := range result.Elevated {
			actions = append(actions, action.Action)
		}
		output += "\nElevated: " + strings.Join(actions, ", ")
	}
	return output
}

func formatDNSTeardown(result dnsTeardownResult) string {
	output := fmt.Sprintf("DNS teardown: %s\nResolver: %s\nState: %s", result.Reference, result.Uninstall.ResolverPath, result.Uninstall.StatePath)
	if len(result.Elevated) > 0 {
		actions := make([]string, 0, len(result.Elevated))
		for _, action := range result.Elevated {
			actions = append(actions, action.Action)
		}
		output += "\nElevated: " + strings.Join(actions, ", ")
	}
	return output
}

func dnsProject(summary tenant.Summary) dns.Tenant {
	return dns.Tenant{
		IncusName:    summary.IncusName,
		InfraProject: summary.InfraProject,
		Tenant:       summary.Tenant,
		DNSSuffix:    summary.DNSSuffix,
		PrivateCIDR:  summary.PrivateCIDR,
	}
}

func runLocalDNSWithSudoFallback(ctx context.Context, config commandConfig, action string, plan localdns.Plan) (localdns.Result, bool, error) {
	result, err := runLocalDNSAction(ctx, config.localDNS, action, plan)
	if err == nil {
		return result, false, nil
	}
	if !isPermissionError(err) {
		return localdns.Result{}, false, err
	}
	if err := runElevatedDNSSubcommand(ctx, config, action, plan.Reference); err != nil {
		return localdns.Result{}, false, fmt.Errorf("%s local DNS requires privileges and sudo failed: %w", action, err)
	}
	return localdns.Result{
		Reference:    plan.Reference,
		Action:       action,
		StatePath:    plan.StatePath,
		ResolverPath: plan.ResolverPath,
	}, true, nil
}

func runLocalDNSAction(ctx context.Context, manager localdns.Manager, action string, plan localdns.Plan) (localdns.Result, error) {
	switch action {
	case "install":
		return manager.Install(ctx, plan)
	case "refresh":
		return manager.Refresh(ctx, plan)
	case "uninstall":
		return manager.Uninstall(ctx, plan)
	default:
		return localdns.Result{}, fmt.Errorf("unknown local DNS action %q", action)
	}
}

func isPermissionError(err error) bool {
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "permission denied")
}

func runElevatedDNSSubcommand(ctx context.Context, config commandConfig, action string, reference string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"env"}
	for _, entry := range sudoPassthroughEnv(config) {
		args = append(args, entry)
	}
	args = append(args, executable, "dns", action, reference)
	command := exec.CommandContext(ctx, "sudo", args...)
	if config.stdin != nil {
		command.Stdin = config.stdin
	}
	if config.stderr != nil {
		command.Stdout = config.stderr
		command.Stderr = config.stderr
	}
	return command.Run()
}

func sudoPassthroughEnv(config commandConfig) []string {
	env := []string{}
	add := func(key string, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			env = append(env, key+"="+value)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		add("HOME", home)
	}
	add("SANDCASTLE_REMOTE", config.adminConfig.Remote)
	add("SANDCASTLE_TENANT", config.adminConfig.Tenant)
	add("SANDCASTLE_PROJECT", config.adminConfig.Project)
	add("SANDCASTLE_ADMIN_REMOTE", config.adminConfig.AdminRemote)
	for _, key := range []string{
		"INCUS_CONF",
		"SANDCASTLE_LOCAL_DNS_STATE",
		"SANDCASTLE_RESOLVER_DIR",
		"SANDCASTLE_BIN",
	} {
		add(key, os.Getenv(key))
	}
	return env
}

func elevatedActions(names []string, flags ...bool) []dnsElevatedActionResult {
	results := []dnsElevatedActionResult{}
	for index, elevated := range flags {
		if !elevated {
			continue
		}
		name := "local-dns"
		if index < len(names) {
			name = names[index]
		}
		results = append(results, dnsElevatedActionResult{Action: name})
	}
	return results
}
