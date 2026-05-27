package cli

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	osuser "os/user"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/authapp"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/domain"
	"github.com/thieso2/sandcastle-incus/internal/images"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/infra"
	"github.com/thieso2/sandcastle-incus/internal/localtrust"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/route"
	"github.com/thieso2/sandcastle-incus/internal/routebroker"
	"github.com/thieso2/sandcastle-incus/internal/share"
	"github.com/thieso2/sandcastle-incus/internal/tailscale"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
	_ "modernc.org/sqlite"
)

func newAdminCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	admin := &cobra.Command{
		Use:   "admin",
		Short: "Run Sandcastle administrator commands",
	}
	admin.AddCommand(newAdminVersionCommand(config, opts))
	admin.AddCommand(newAdminTenantCommand(config, opts))
	admin.AddCommand(newAdminUserCommand(config, opts))
	admin.AddCommand(newAdminInfraCommand(config, opts))
	admin.AddCommand(newAdminImageCommand(config, opts))
	admin.AddCommand(newAdminTLDCommand(config, opts))
	admin.AddCommand(newAdminRouteBrokerCommand(config))
	admin.AddCommand(newAdminAuthAppCommand(config))
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

func newAdminTenantCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "tenant",
		Short: "Manage Sandcastle tenants",
	}
	command.AddCommand(newAdminTenantListCommand(config, opts))
	command.AddCommand(newAdminTenantStatusCommand(config, opts))
	command.AddCommand(newAdminTenantCreateCommand(config, opts))
	command.AddCommand(newAdminTenantDeleteCommand(config, opts))
	command.AddCommand(newAdminTenantGrantCommand(config, opts))
	command.AddCommand(newAdminTenantRevokeCommand(config, opts))
	command.AddCommand(newAdminTenantUsersCommand(config, opts))
	command.AddCommand(newAdminTenantSetSSHKeyCommand(config))
	return command
}

func newAdminTenantListCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "list [tenant]",
		Short: "List Sandcastle tenants, or all resources in a specific tenant",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tenants, err := listTenants(cmd.Context(), config.tenantStore)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				payload := tenantListPayload{Tenants: tenants}
				return writeOutput(config.stdout, opts.output, formatTenantList(tenants), payload)
			}
			ref := strings.TrimSpace(args[0])
			var summary tenant.Summary
			found := false
			for _, t := range tenants {
				if t.Tenant == ref {
					summary = t
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("Sandcastle tenant %s not found", ref)
			}
			if config.machineStore == nil {
				return fmt.Errorf("machine metadata store is not configured")
			}
			machines, unmanaged, err := listMachinesAndUnmanaged(cmd.Context(), config.machineStore, summary)
			if err != nil {
				return err
			}
			result := tenantResourcesPayload{
				Tenant:    summary,
				Machines:  machines,
				Unmanaged: unmanaged,
			}
			return writeOutput(config.stdout, opts.output, formatTenantResources(result), result)
		},
	}
}

func newAdminTenantStatusCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status tenant",
		Short: "Show Sandcastle tenant status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			status, err := tenant.GetStatusWithTopology(
				cmd.Context(),
				config.tenantStore,
				config.topologyStore,
				tenant.TopologyRequest{},
				args[0],
			)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatTenantStatus(status), status)
		},
	}
}

func newAdminTenantCreateCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	var sshKey string
	var tailscaleAuthKey string
	command := &cobra.Command{
		Use:   "create tenant",
		Short: "Create a Sandcastle tenant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var occupiedCIDRs []string
			if !dryRun {
				existingTenants, err := listTenants(cmd.Context(), config.tenantStore)
				if err != nil {
					return err
				}
				occupiedCIDRs = tenant.OccupiedCIDRs(existingTenants)
			}
			plan, err := tenant.PlanCreate(config.adminConfig, tenant.CreateRequest{
				Reference:     args[0],
				SSHPublicKey:  sshKey,
				OccupiedCIDRs: occupiedCIDRs,
			})
			if err != nil {
				return err
			}
			if !dryRun {
				if config.tenantCreator == nil {
					return fmt.Errorf("tenant creation executor is not configured")
				}
				incusx.NewConnectCache(config.adminConfig.Remote).InvalidateTenant(plan.Reference)
				incusx.InvalidateTenantCA(config.adminConfig.Remote, plan.IncusProject)
				if err := config.tenantCreator.CreateTenant(cmd.Context(), plan); err != nil {
					return err
				}
				authKey := strings.TrimSpace(tailscaleAuthKey)
				if authKey == "" {
					authKey = strings.TrimSpace(config.adminConfig.AuthTailscaleAuthKey)
				}
				if config.tailscale != nil {
					tsAdmin := config.adminConfig
					tsAdmin.Tenant = plan.Reference
					upPlan, err := tailscalePlanUpForTenant(cmd.Context(), tsAdmin, config.tenantStore, authKey)
					if err != nil {
						fmt.Fprintf(config.stderr, "Warning: tailscale up plan failed: %v\n", err)
					} else if err := config.tailscale.RunUp(cmd.Context(), upPlan, tailscale.RunSession{
						Stdout: config.stdout,
						Stderr: config.stderr,
					}); err != nil {
						fmt.Fprintf(config.stderr, "Warning: tailscale up failed: %v\n", err)
					}
				}
			}
			return writeOutput(config.stdout, opts.output, formatCreatePlan(plan), plan)
		},
	}
	command.Flags().StringVar(&sshKey, "ssh-key", "", "SSH public key to inject into all tenant machines")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the Incus creation plan without mutating resources")
	command.Flags().StringVar(&tailscaleAuthKey, "tailscale-auth-key", "", "Tailscale auth key; if set, runs tailscale up after tenant creation")
	return command
}

func tailscalePlanUpForTenant(ctx context.Context, admin scconfig.Admin, store tenant.IncusTenantStore, authKey string) (tailscale.UpPlan, error) {
	return tailscale.PlanUp(ctx, admin, store, tailscale.UpRequest{
		Reference:     admin.Tenant,
		AuthKey:       authKey,
		AdvertiseTags: defaultAdvertiseTags(),
	})
}

func formatCreatePlan(plan tenant.CreatePlan) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Tenant: %s\n", plan.Reference)
	fmt.Fprintf(&builder, "Incus project: %s\n", plan.IncusProject)
	fmt.Fprintf(&builder, "DNS suffix: %s\n", plan.DNSSuffix)
	fmt.Fprintf(&builder, "Private CIDR: %s\n", plan.PrivateCIDR)
	fmt.Fprintf(&builder, "Network: %s\n", plan.PrivateNetwork)
	fmt.Fprintf(&builder, "Volumes: %s, %s, %s\n", plan.HomeVolume, plan.WorkspaceVolume, plan.CAVolume)
	fmt.Fprintf(&builder, "Sidecars: %s (%s), %s (%s)", plan.TailscaleInstance, plan.TailscaleAddress, plan.DNSInstance, plan.DNSAddress)
	return builder.String()
}

func newAdminTenantDeleteCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var yes bool
	var purge bool
	command := &cobra.Command{
		Use:   "delete tenant",
		Short: "Delete a Sandcastle tenant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				confirmed, err := confirmMissingYes(config, "Delete tenant "+args[0]+"?", "refusing to delete without --yes")
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("delete canceled")
				}
			}
			plan, err := tenant.PlanDelete(config.adminConfig, tenant.DeleteRequest{
				Reference: args[0],
				Purge:     purge,
			})
			if err != nil {
				return err
			}
			if config.shareStore != nil {
				cleanup, err := share.CleanupTenantDeletion(cmd.Context(), config.tenantStore, config.shareStore, share.TenantCleanupRequest{Tenant: args[0]})
				if err != nil {
					return err
				}
				if config.shareReconciler != nil {
					summaries, err := listTenants(cmd.Context(), config.tenantStore)
					if err != nil {
						return err
					}
					for _, recipient := range cleanup.AffectedRecipients {
						summary, ok := findTenantSummaryForCleanup(summaries, recipient)
						if !ok {
							continue
						}
						result, err := config.shareReconciler.ReconcileTenantShares(cmd.Context(), summary, false)
						if err != nil {
							return err
						}
						if result.HasFailures() {
							return fmt.Errorf("share cleanup reconciliation failed for tenant %s", recipient)
						}
					}
				}
			}
			if config.tenantDeleter == nil {
				return fmt.Errorf("tenant deletion executor is not configured")
			}
			if err := config.tenantDeleter.DeleteTenant(cmd.Context(), plan); err != nil {
				return err
			}
			incusx.NewConnectCache(config.adminConfig.Remote).InvalidateTenant(plan.Reference)
			incusx.InvalidateTenantCA(config.adminConfig.Remote, plan.IncusProject)
			return writeOutput(config.stdout, opts.output, formatDeletePlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&yes, "yes", false, "confirm tenant deletion")
	command.Flags().BoolVar(&purge, "purge", false, "delete durable tenant volumes and the Incus project")
	return command
}

func findTenantSummaryForCleanup(summaries []tenant.Summary, tenantName string) (tenant.Summary, bool) {
	for _, summary := range summaries {
		if summary.Tenant == tenantName {
			return summary, true
		}
	}
	return tenant.Summary{}, false
}

func newAdminTenantSetSSHKeyCommand(config commandConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "set-ssh-key tenant key",
		Short: "Set or update the SSH public key for a Sandcastle tenant",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if config.tenantSSHKeyUpdater == nil {
				return fmt.Errorf("tenant SSH key updater is not configured")
			}
			ref, err := tenant.ParseRef(config.adminConfig, args[0])
			if err != nil {
				return err
			}
			return config.tenantSSHKeyUpdater.SetTenantSSHKey(cmd.Context(), ref.IncusProject, args[1])
		},
	}
}

func newAdminTenantGrantCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "grant tenant user",
		Short: "Grant tenant access to a restricted user",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := usertrust.PlanTenantGrant(config.adminConfig, usertrust.TenantAccessRequest{
				Tenant: args[0],
				User:   args[1],
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

func newAdminTenantRevokeCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "revoke tenant user",
		Short: "Revoke tenant access from a restricted user",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := usertrust.PlanTenantRevoke(config.adminConfig, usertrust.TenantAccessRequest{
				Tenant: args[0],
				User:   args[1],
			})
			if err != nil {
				return err
			}
			if !dryRun {
				if config.trustManager == nil {
					return fmt.Errorf("restricted user revoke executor is not configured")
				}
				if err := config.trustManager.Revoke(cmd.Context(), plan); err != nil {
					return err
				}
			}
			return writeOutput(config.stdout, opts.output, formatUserPlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the revoke plan without mutating trust state")
	return command
}

func newAdminTenantUsersCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "users tenant",
		Short: "List restricted users with tenant access",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := usertrust.PlanTenantUsers(config.adminConfig, args[0])
			if err != nil {
				return err
			}
			if config.trustManager == nil {
				return fmt.Errorf("restricted user list executor is not configured")
			}
			result, err := config.trustManager.ListTenantUsers(cmd.Context(), plan)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatTenantUsers(result), result)
		},
	}
}

func formatTenantUsers(result usertrust.TenantUsersResult) string {
	if len(result.Users) == 0 {
		return fmt.Sprintf("Tenant: %s\nUsers: none", result.Tenant)
	}
	return fmt.Sprintf("Tenant: %s\nUsers: %s", result.Tenant, strings.Join(result.Users, ", "))
}

func formatDeletePlan(plan tenant.DeletePlan) string {
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
	infraCommand.AddCommand(newAdminInfraGenSeedCommand(config, opts))
	infraCommand.AddCommand(newAdminInfraDeleteCommand(config, opts))
	infraCommand.AddCommand(newAdminInfraCertCommand(config, opts))
	infraCommand.AddCommand(newAdminInfraTrustCommand(config, opts))
	return infraCommand
}

func newAdminInfraCreateCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	var unixUser string
	var deploymentName string
	var seedPath string
	var debugDeviceUser string
	var tailscaleAuthKey string
	command := &cobra.Command{
		Use:   "create",
		Short: "Create Sandcastle shared infrastructure",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			seedState, err := loadOrCreateInfraSeed(config.adminConfig, deploymentName, seedPath, resolveInfraSeedUsername(cmd, unixUser))
			if err != nil {
				return err
			}
			if os.Getenv("VERBOSE") == "1" {
				seedStatus := "not found (will create on first run)"
				if seedState.exists {
					seedStatus = "loaded"
				}
				fmt.Fprintf(config.stderr, "[verbose] seed file: %s (%s)\n", seedState.path, seedStatus)
			}
			username := resolveInfraSeedUsername(cmd, unixUser)
			admin := config.adminConfig
			if seedState.exists {
				admin = infra.ResolveSeedAdmin(seedState.seed)
			}
			if strings.TrimSpace(debugDeviceUser) != "" {
				admin.AuthDebugDeviceUser = strings.TrimSpace(debugDeviceUser)
			}
			if strings.TrimSpace(tailscaleAuthKey) != "" {
				admin.AuthTailscaleAuthKey = strings.TrimSpace(tailscaleAuthKey)
			}
			plan, err := infra.PlanCreate(admin, infra.CreateRequest{UnixUser: username})
			if err != nil {
				return err
			}
			plan.Remote = admin.Remote
			plan.DeploymentName = seedState.deployment
			plan.SeedPath = seedState.path
			if seedState.exists && scconfig.AdminEnvValue("SANDCASTLE_INFRA_CADDY_DATA_ARCHIVE") == "" {
				plan.CaddyDataArchivePath = ""
			}
			cleanup, err := applySeedCaddyDataArchive(seedState.seed, admin, &plan)
			if err != nil {
				return err
			}
			defer cleanup()
			runConfig := config
			runConfig.adminConfig = admin
			if !dryRun {
				if err := saveInfraSeedIfMissing(seedState); err != nil {
					return err
				}
				if err := prepareInfrastructureImages(cmd.Context(), runConfig); err != nil {
					return err
				}
				if runConfig.infraCreator == nil {
					return fmt.Errorf("infrastructure creation executor is not configured")
				}
				if shouldInstallInfraTrustAfterCreate(plan) {
					ca, err := infra.LoadOrCreatePersistentInternalCA(admin)
					if err != nil {
						return err
					}
					plan = infra.ApplyInternalCA(plan, ca)
				}
				writeInfraConfigBanner(runConfig, opts)
				if err := runConfig.infraCreator.CreateInfrastructure(cmd.Context(), plan); err != nil {
					return err
				}
				if shouldInstallInfraTrustAfterCreate(plan) {
					result, err := installInfraTrust(cmd.Context(), runConfig, opts)
					if err != nil {
						fmt.Fprintf(config.stderr, "Warning: infrastructure debug CA trust install failed: %v\nRun ./bin/sc-adm infra trust install interactively to install the cached CA.\n", err)
					} else if opts.output == outputText {
						fmt.Fprintln(config.stdout, formatInfraTrustResult(result))
					}
				}
				if err := captureInfraSeedCaddyData(cmd.Context(), runConfig, seedState.seed, seedState.path, plan); err != nil {
					return err
				}
			}
			return writeOutput(config.stdout, opts.output, formatInfraCreatePlan(plan), redactedInfraCreatePlan(plan))
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the infrastructure creation plan without mutating resources")
	command.Flags().StringVar(&unixUser, "username", "", "Unix username assigned to machines provisioned through this Auth App")
	command.Flags().StringVar(&deploymentName, "name", "", "deployment name for the default seed path")
	command.Flags().StringVar(&seedPath, "seed", "", "infrastructure seed file path")
	command.Flags().StringVar(&debugDeviceUser, "debug-device-user", "", "enable debug device approval as this allowlisted GitHub username")
	command.Flags().StringVar(&tailscaleAuthKey, "tailscale-auth-key", "", "Tailscale auth key returned to approved CLI device logins for unattended tenant attachment")
	return command
}

type infraSeedState struct {
	deployment string
	path       string
	seed       infra.Seed
	exists     bool
}

func loadOrCreateInfraSeed(admin scconfig.Admin, deploymentName string, seedPath string, _ string) (infraSeedState, error) {
	deployment := strings.TrimSpace(deploymentName)
	if deployment == "" {
		deployment = infra.DefaultDeploymentName(admin)
	}
	if err := infra.ValidateDeploymentName(deployment); err != nil {
		return infraSeedState{}, err
	}
	path := strings.TrimSpace(seedPath)
	if path == "" {
		var err error
		path, err = infra.DefaultSeedPath(deployment)
		if err != nil {
			return infraSeedState{}, err
		}
	}
	seed, exists, err := infra.LoadSeed(path)
	if err != nil {
		return infraSeedState{}, fmt.Errorf("load infrastructure seed %s: %w", path, err)
	}
	if exists {
		if strings.TrimSpace(seed.Deployment) == "" {
			seed.Deployment = deployment
		}
		if err := infra.ValidateDeploymentName(seed.Deployment); err != nil {
			return infraSeedState{}, err
		}
		return infraSeedState{deployment: seed.Deployment, path: path, seed: seed, exists: true}, nil
	}
	seed = infra.SeedFromAdmin(deployment, admin)
	seed, err = infra.EmbedExistingCaddyDataArchive(seed, admin)
	if err != nil {
		return infraSeedState{}, err
	}
	return infraSeedState{deployment: deployment, path: path, seed: seed}, nil
}

func defaultLocalUnixUsername() string {
	if value := strings.TrimSpace(os.Getenv("USER")); value != "" {
		return value
	}
	if current, err := osuser.Current(); err == nil && current != nil {
		return strings.TrimSpace(current.Username)
	}
	return ""
}

func resolveInfraSeedUsername(cmd *cobra.Command, flagValue string) string {
	if cmd != nil && cmd.Flags().Changed("username") {
		return strings.TrimSpace(flagValue)
	}
	if value := strings.TrimSpace(os.Getenv("SANDCASTLE_AUTH_DEFAULT_UNIX_USER")); value != "" {
		return value
	}
	return defaultLocalUnixUsername()
}

func formatInfraCreatePlan(plan infra.CreatePlan) string {
	return fmt.Sprintf("Infrastructure project: %s\nRuntime: %s, %s\nDefault Unix user: %s\nSeed: %s", plan.Project, plan.CaddyInstance, plan.RouteBrokerInstance, bannerValue(plan.DefaultUnixUser), bannerValue(plan.SeedPath))
}

func saveInfraSeedIfMissing(state infraSeedState) error {
	if state.exists {
		return nil
	}
	if err := infra.SaveSeed(state.path, state.seed); err != nil {
		return fmt.Errorf("write infrastructure seed %s: %w", state.path, err)
	}
	return nil
}

func applySeedCaddyDataArchive(seed infra.Seed, admin scconfig.Admin, plan *infra.CreatePlan) (func(), error) {
	if !strings.EqualFold(strings.TrimSpace(plan.TLSMode), "acme") {
		return func() {}, nil
	}
	data, ok, err := infra.CaddyDataArchiveBytes(seed, admin.AuthHostname)
	if err != nil {
		return func() {}, err
	}
	if !ok {
		return func() {}, nil
	}
	if strings.TrimSpace(plan.CaddyDataArchivePath) != "" {
		return func() {}, nil
	}
	file, err := os.CreateTemp("", "sandcastle-caddy-data-*.tgz")
	if err != nil {
		return func() {}, err
	}
	path := file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return func() {}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return func() {}, err
	}
	plan.CaddyDataArchivePath = path
	return func() { _ = os.Remove(path) }, nil
}

func captureInfraSeedCaddyData(ctx context.Context, config commandConfig, seed infra.Seed, seedPath string, plan infra.CreatePlan) error {
	if !strings.EqualFold(strings.TrimSpace(plan.TLSMode), "acme") || config.infraCaddyData == nil {
		return nil
	}
	file, err := os.CreateTemp("", "sandcastle-caddy-data-export-*.tgz")
	if err != nil {
		return err
	}
	path := file.Name()
	_ = file.Close()
	defer os.Remove(path)
	_, err = config.infraCaddyData.ExportCaddyData(ctx, infra.CaddyDataExportPlan{
		Remote:      plan.Remote,
		Project:     plan.Project,
		Instance:    plan.CaddyInstance,
		SourcePath:  infra.CaddyDataDir,
		ArchivePath: path,
	})
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	seed = infra.EmbedCaddyDataArchive(seed, config.adminConfig.AuthHostname, data)
	if err := infra.SaveSeed(seedPath, seed); err != nil {
		return fmt.Errorf("update infrastructure seed %s: %w", seedPath, err)
	}
	return nil
}

func redactedInfraCreatePlan(plan infra.CreatePlan) infra.CreatePlan {
	redacted := plan
	redacted.RuntimeFiles = append([]infra.RuntimeFile{}, plan.RuntimeFiles...)
	for i := range redacted.RuntimeFiles {
		if redacted.RuntimeFiles[i].Mode&0o077 == 0 {
			redacted.RuntimeFiles[i].Content = "redacted"
			continue
		}
		if redacted.RuntimeFiles[i].Path == infra.AuthAppEnvPath {
			redacted.RuntimeFiles[i].Content = redactEnvContent(redacted.RuntimeFiles[i].Content, []string{
				"SANDCASTLE_AUTH_GITHUB_CLIENT_SECRET",
				"SANDCASTLE_AUTH_TAILSCALE_AUTHKEY",
			})
		}
	}
	return redacted
}

func redactEnvContent(content string, keys []string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		for _, key := range keys {
			if strings.HasPrefix(line, key+"=") {
				lines[i] = key + "='redacted'"
			}
		}
	}
	return strings.Join(lines, "\n")
}

func newAdminInfraGenSeedCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var deploymentName string
	var seedPath string
	var unixUser string
	command := &cobra.Command{
		Use:   "gen-seed",
		Short: "Generate a Sandcastle infrastructure seed file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			username := resolveInfraSeedUsername(cmd, unixUser)
			state, err := loadOrCreateInfraSeed(config.adminConfig, deploymentName, seedPath, username)
			if err != nil {
				return err
			}
			generated := infra.SeedFromAdmin(state.deployment, config.adminConfig)
			if state.exists {
				generated.TLS = state.seed.TLS
			} else {
				generated, err = infra.EmbedExistingCaddyDataArchive(generated, config.adminConfig)
				if err != nil {
					return err
				}
			}
			state.seed = generated
			if err := infra.SaveSeed(state.path, state.seed); err != nil {
				return fmt.Errorf("write infrastructure seed %s: %w", state.path, err)
			}
			result := infraSeedResult{
				Deployment: state.seed.Deployment,
				SeedPath:   state.path,
				Written:    true,
				Exists:     state.exists,
			}
			return writeOutput(config.stdout, opts.output, formatInfraSeedResult(result), result)
		},
	}
	command.Flags().StringVar(&deploymentName, "name", "", "deployment name for the default seed path")
	command.Flags().StringVar(&seedPath, "seed", "", "infrastructure seed file path")
	command.Flags().StringVar(&unixUser, "username", "", "Unix username assigned to machines provisioned through this Auth App")
	return command
}

type infraSeedResult struct {
	Deployment string `json:"deployment"`
	SeedPath   string `json:"seedPath"`
	Written    bool   `json:"written"`
	Exists     bool   `json:"existed"`
}

func formatInfraSeedResult(result infraSeedResult) string {
	status := "Generated"
	if result.Exists {
		status = "Updated"
	}
	return fmt.Sprintf("%s infrastructure seed\nDeployment: %s\nSeed: %s", status, result.Deployment, result.SeedPath)
}

func shouldInstallInfraTrustAfterCreate(plan infra.CreatePlan) bool {
	return strings.EqualFold(strings.TrimSpace(plan.TLSMode), "internal")
}

func prepareInfrastructureImages(ctx context.Context, config commandConfig) error {
	if config.imageBuilder == nil || config.imageUploader == nil {
		return nil
	}
	for _, template := range []string{"base", "ai"} {
		imageRef, err := infrastructureImageRef(config.adminConfig, template)
		if err != nil {
			return err
		}
		if isFullImageSource(imageRef) {
			if os.Getenv("VERBOSE") == "1" {
				fmt.Fprintf(config.stderr, "[infra-create] prepare image %s: use %s (full OCI source)\n", template, imageRef)
			}
			continue
		}
		uploadPlan, err := images.PlanUpload(config.adminConfig, images.UploadRequest{Template: template, SourceRef: imageRef, Alias: imageRef})
		if err != nil {
			return err
		}
		if shouldReuseRemoteInfrastructureImage(ctx, config.imageUploader, uploadPlan.Remote, uploadPlan.Alias) {
			if os.Getenv("VERBOSE") == "1" {
				fmt.Fprintf(config.stderr, "[infra-create] prepare image %s: use %s from %s (remote image exists)\n", template, imageRef, uploadPlan.Remote)
			}
			continue
		}
		buildPlatform := firstNonEmptyEnvOrDefault("linux/amd64", "SANDCASTLE_IMAGE_PLATFORM", "DOCKER_DEFAULT_PLATFORM")
		buildRequest := images.BuildRequest{Template: template, Tag: imageRef, Platform: buildPlatform}
		if template == "ai" {
			buildRequest.CodexVersion = firstNonEmptyEnvOrDefault("latest", "SANDCASTLE_CODEX_VERSION", "SANDCASTLE_E2E_CODEX_VERSION")
			buildRequest.ClaudeVersion = firstNonEmptyEnvOrDefault("latest", "SANDCASTLE_CLAUDE_VERSION", "SANDCASTLE_E2E_CLAUDE_CODE_VERSION")
			buildRequest.GeminiVersion = firstNonEmptyEnvOrDefault("latest", "SANDCASTLE_GEMINI_VERSION", "SANDCASTLE_E2E_GEMINI_CLI_VERSION")
		}
		buildPlan, err := images.PlanBuild(config.adminConfig, buildRequest)
		if err != nil {
			return err
		}
		if shouldReuseLocalInfrastructureImage(ctx, config.imageBuilder, buildPlan.Tool, imageRef, buildPlatform) {
			if os.Getenv("VERBOSE") == "1" {
				fmt.Fprintf(config.stderr, "[infra-create] prepare image %s: build %s (%s) skipped (local image exists)\n", template, imageRef, buildPlatform)
			}
		} else {
			if err := runInfraCreateVerboseStep(config, fmt.Sprintf("prepare image %s: build %s (%s)", template, imageRef, buildPlatform), func() error {
				_, err := config.imageBuilder.BuildImage(ctx, buildPlan)
				return err
			}); err != nil {
				return err
			}
		}
		if err := runInfraCreateVerboseStep(config, fmt.Sprintf("prepare image %s: upload %s to %s", template, imageRef, uploadPlan.Remote), func() error {
			_, err := config.imageUploader.UploadImage(ctx, uploadPlan)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func shouldReuseLocalInfrastructureImage(ctx context.Context, builder images.Builder, tool string, imageRef string, platform string) bool {
	if os.Getenv("SANDCASTLE_IMAGE_REBUILD") == "1" {
		return false
	}
	switch builder.(type) {
	case images.LocalBuilder:
	default:
		return false
	}
	arch, ok := localImageArchitecture(ctx, tool, imageRef)
	if !ok {
		return false
	}
	want := strings.TrimPrefix(strings.TrimSpace(platform), "linux/")
	return want != "" && arch == want
}

func shouldReuseRemoteInfrastructureImage(ctx context.Context, uploader images.Uploader, remote string, alias string) bool {
	if os.Getenv("SANDCASTLE_IMAGE_REUPLOAD") == "1" {
		return false
	}
	switch uploader.(type) {
	case images.LocalUploader:
	default:
		return false
	}
	return remoteImageExists(ctx, remote, alias)
}

func localImageArchitecture(ctx context.Context, tool string, imageRef string) (string, bool) {
	if strings.TrimSpace(tool) == "" {
		tool = "docker"
	}
	output, err := exec.CommandContext(ctx, tool, "image", "inspect", imageRef, "--format", "{{.Architecture}}").Output()
	if err != nil {
		return "", false
	}
	arch := strings.TrimSpace(string(output))
	return arch, arch != ""
}

func remoteImageExists(ctx context.Context, remote string, alias string) bool {
	remote = strings.TrimSpace(remote)
	alias = strings.TrimSpace(alias)
	if remote == "" || alias == "" {
		return false
	}
	return exec.CommandContext(ctx, "incus", "image", "info", remote+":"+alias).Run() == nil
}

func runInfraCreateVerboseStep(config commandConfig, label string, fn func() error) error {
	if os.Getenv("VERBOSE") != "1" {
		return fn()
	}
	start := time.Now()
	fmt.Fprintf(config.stderr, "[infra-create] %s ...", label)
	if err := fn(); err != nil {
		fmt.Fprintf(config.stderr, " failed (%s)\n", cliVerboseDuration(time.Since(start)))
		return err
	}
	fmt.Fprintf(config.stderr, " done (%s)\n", cliVerboseDuration(time.Since(start)))
	return nil
}

func cliVerboseDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return fmt.Sprintf("%dus", duration.Microseconds())
	}
	return duration.Round(time.Millisecond).String()
}

func infrastructureImageRef(admin scconfig.Admin, template string) (string, error) {
	switch template {
	case "base":
		return strings.TrimSpace(admin.Images.Base), nil
	case "ai":
		return strings.TrimSpace(admin.Images.AI), nil
	default:
		return "", fmt.Errorf("unknown image template %q", template)
	}
}

func isFullImageSource(ref string) bool {
	return strings.HasPrefix(strings.TrimSpace(ref), "oci:")
}

func firstNonEmptyEnvOrDefault(fallback string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return fallback
}

func newAdminInfraDeleteCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var yes bool
	var purge bool
	command := &cobra.Command{
		Use:   "delete",
		Short: "Delete Sandcastle shared infrastructure",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = yes
			plan, err := infra.PlanDelete(config.adminConfig, infra.DeleteRequest{Purge: purge})
			if err != nil {
				return err
			}
			if config.infraDeleter == nil {
				return fmt.Errorf("infrastructure deletion executor is not configured")
			}
			writeInfraConfigBanner(config, opts)
			if err := config.infraDeleter.DeleteInfrastructure(cmd.Context(), plan); err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatInfraDeletePlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&yes, "yes", false, "accepted for compatibility; infrastructure deletion is confirmed by running the command")
	command.Flags().BoolVar(&purge, "purge", false, "also delete Sandcastle tenant projects and durable data")
	return command
}

func formatInfraDeletePlan(plan infra.DeletePlan) string {
	if plan.PurgeData {
		return fmt.Sprintf("Deleted infrastructure project: %s and purged Sandcastle data.", plan.Project)
	}
	return fmt.Sprintf("Deleted infrastructure project: %s", plan.Project)
}

func newAdminInfraCertCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "cert",
		Short: "Manage reusable infrastructure Caddy ACME certificate data",
	}
	command.AddCommand(newAdminInfraCertExportCommand(config, opts))
	return command
}

func newAdminInfraCertExportCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var archivePath string
	var dryRun bool
	command := &cobra.Command{
		Use:   "export",
		Short: "Export working infrastructure Caddy ACME data for reuse on infra create",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := infra.PlanCaddyDataExport(config.adminConfig, infra.CaddyDataExportRequest{ArchivePath: archivePath})
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatInfraCertExportPlan(plan), plan)
			}
			if config.infraCaddyData == nil {
				return fmt.Errorf("infrastructure Caddy data exporter is not configured")
			}
			result, err := config.infraCaddyData.ExportCaddyData(cmd.Context(), plan)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatInfraCertExportResult(result), result)
		},
	}
	command.Flags().StringVar(&archivePath, "archive", "", "archive path, defaulting to the configured Sandcastle Caddy data cache")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the Caddy data export plan without downloading it")
	return command
}

func formatInfraCertExportPlan(plan infra.CaddyDataExportPlan) string {
	return fmt.Sprintf("Export infrastructure Caddy ACME data\nSource: %s:%s%s\nArchive: %s", plan.Project, plan.Instance, plan.SourcePath, plan.ArchivePath)
}

func formatInfraCertExportResult(result infra.CaddyDataExportResult) string {
	return fmt.Sprintf("Exported infrastructure Caddy ACME data\nSource: %s:%s%s\nArchive: %s", result.Project, result.Instance, result.SourcePath, result.ArchivePath)
}

func newAdminInfraTrustCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "trust",
		Short: "Manage local trust for infrastructure debug TLS",
	}
	command.AddCommand(newAdminInfraTrustInstallCommand(config, opts))
	command.AddCommand(newAdminInfraTrustUninstallCommand(config, opts))
	return command
}

func newAdminInfraTrustInstallCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "install",
		Short: "Install the infrastructure debug TLS CA into local trust",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := planInfraTrust(config)
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatInfraTrustPlan("Install", plan), plan)
			}
			result, err := installInfraTrust(cmd.Context(), config, opts)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatInfraTrustResult(result), result)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the infrastructure trust install plan without changing local trust")
	return command
}

func newAdminInfraTrustUninstallCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the infrastructure debug TLS CA from local trust",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := planInfraTrust(config)
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatInfraTrustPlan("Uninstall", plan), plan)
			}
			if config.localTrust == nil {
				return fmt.Errorf("local trust executor is not configured")
			}
			result, err := config.localTrust.Uninstall(cmd.Context(), plan)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatInfraTrustResult(result), result)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the infrastructure trust uninstall plan without changing local trust")
	return command
}

func installInfraTrust(ctx context.Context, config commandConfig, opts *rootOptions) (localtrust.Result, error) {
	plan, err := planInfraTrust(config)
	if err != nil {
		return localtrust.Result{}, err
	}
	if config.localTrust == nil {
		return localtrust.Result{}, fmt.Errorf("local trust executor is not configured")
	}
	if err := writeTrustWarning(config, opts, plan); err != nil {
		return localtrust.Result{}, err
	}
	return config.localTrust.Install(ctx, plan)
}

func planInfraTrust(config commandConfig) (localtrust.Plan, error) {
	admin := config.adminConfig
	if err := admin.Validate(); err != nil {
		return localtrust.Plan{}, err
	}
	return localtrust.Plan{
		Reference:       "infrastructure",
		IncusProject:    admin.InfrastructureProject,
		Instance:        route.InfrastructureCaddyName,
		CertificatePath: infra.CaddyPKIRootCertPath,
		TrustName:       "Sandcastle infrastructure debug CA",
		Platform:        "",
		Warning:         "Trusting the infrastructure debug CA allows this Sandcastle infrastructure Caddy to mint certificates trusted by this machine. Use only with SANDCASTLE_INFRA_TLS_MODE=internal.",
	}, nil
}

func formatInfraTrustPlan(action string, plan localtrust.Plan) string {
	return fmt.Sprintf("%s infrastructure debug CA trust\nCA: %s:%s%s\nWarning: %s", action, plan.IncusProject, plan.Instance, plan.CertificatePath, plan.Warning)
}

func formatInfraTrustResult(result localtrust.Result) string {
	if result.Target == "" {
		return fmt.Sprintf("%s infrastructure debug CA trust", result.Action)
	}
	return fmt.Sprintf("%s infrastructure debug CA trust\nTarget: %s", result.Action, result.Target)
}

func writeInfraConfigBanner(config commandConfig, opts *rootOptions) {
	if config.stderr == nil || opts.output == outputJSON {
		return
	}
	admin := config.adminConfig
	secretState := "unset"
	if strings.TrimSpace(admin.AuthGitHubClientSecret) != "" {
		secretState = "set (redacted)"
	}
	socket := strings.TrimSpace(admin.RouteBrokerIncusSocket)
	if socket == "" {
		socket = "unset"
	}
	authAdmins := strings.Join(admin.AuthAdminGitHubUsers, ",")
	if authAdmins == "" {
		authAdmins = "unset"
	}
	fmt.Fprintf(config.stderr, `Sandcastle infrastructure configuration
  SANDCASTLE_REMOTE=%s
  SANDCASTLE_STORAGE_POOL=%s
  SANDCASTLE_CIDR_POOL=%s
  SANDCASTLE_INCUS_PROJECT_PREFIX=%s
  SANDCASTLE_INFRA_PROJECT=%s
  SANDCASTLE_INFRA_HOST=%s
  SANDCASTLE_LETSENCRYPT_EMAIL=%s
  SANDCASTLE_INFRA_TLS_MODE=%s
  SANDCASTLE_INFRA_CADDY_DATA_ARCHIVE=%s
  SANDCASTLE_BASE_IMAGE=%s
  SANDCASTLE_AI_IMAGE=%s
  SANDCASTLE_ADMIN_BIN=%s
  SANDCASTLE_BIN=%s
  selected admin binary=%s
  SANDCASTLE_AUTH_HOSTNAME=%s
  SANDCASTLE_AUTH_GITHUB_CLIENT_ID=%s
  SANDCASTLE_AUTH_GITHUB_CLIENT_SECRET=%s
  SANDCASTLE_AUTH_ADMIN_GITHUB_USERS=%s
  SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET=%s
`,
		bannerValue(admin.Remote),
		bannerValue(admin.StoragePool),
		bannerValue(admin.CIDRPool),
		bannerValue(admin.IncusProjectPrefix),
		bannerValue(admin.InfrastructureProject),
		bannerValue(admin.InfrastructureHost),
		bannerValue(admin.LetsEncryptEmail),
		bannerValue(admin.InfrastructureTLSMode),
		bannerValue(infra.CaddyDataArchivePath(admin)),
		bannerValue(admin.Images.Base),
		bannerValue(admin.Images.AI),
		bannerValue(os.Getenv("SANDCASTLE_ADMIN_BIN")),
		bannerValue(os.Getenv("SANDCASTLE_BIN")),
		bannerValue(infra.RuntimeBinarySource()),
		bannerValue(admin.AuthHostname),
		bannerValue(admin.AuthGitHubClientID),
		secretState,
		authAdmins,
		socket,
	)
}

func bannerValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unset"
	}
	return value
}

func newAdminImageCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	imageCommand := &cobra.Command{
		Use:   "image",
		Short: "Manage Sandcastle image aliases",
	}
	imageCommand.AddCommand(newAdminImageBuildCommand(config, opts))
	imageCommand.AddCommand(newAdminImageImportCommand(config, opts))
	imageCommand.AddCommand(newAdminImageSyncCommand(config, opts))
	return imageCommand
}

func newAdminImageBuildCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var tag string
	var tool string
	var platform string
	var codexVersion string
	var claudeVersion string
	var geminiVersion string
	var dryRun bool
	command := &cobra.Command{
		Use:   "build base|ai",
		Short: "Build a Sandcastle OCI image",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := images.PlanBuild(config.adminConfig, images.BuildRequest{
				Template:      args[0],
				Tag:           tag,
				Tool:          tool,
				Platform:      platform,
				CodexVersion:  codexVersion,
				ClaudeVersion: claudeVersion,
				GeminiVersion: geminiVersion,
			})
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatImageBuildPlan(plan), plan)
			}
			if config.imageBuilder == nil {
				return fmt.Errorf("image build executor is not configured")
			}
			result, err := config.imageBuilder.BuildImage(cmd.Context(), plan)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatImageBuildResult(result), result)
		},
	}
	command.Flags().StringVar(&tag, "tag", "", "image tag to build, defaulting to the configured Sandcastle image alias")
	command.Flags().StringVar(&tool, "tool", "docker", "OCI image build tool")
	command.Flags().StringVar(&platform, "platform", "", "OCI image build platform, for example linux/amd64")
	command.Flags().StringVar(&codexVersion, "codex-version", "", "pinned Codex CLI version for AI images")
	command.Flags().StringVar(&claudeVersion, "claude-version", "", "pinned Claude Code version for AI images")
	command.Flags().StringVar(&geminiVersion, "gemini-version", "", "pinned Gemini CLI version for AI images")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the image build command without running it")
	return command
}

func newAdminImageSyncCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "sync image-ref",
		Short: "Sync an imported image into a Sandcastle image alias",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := images.PlanSync(config.adminConfig, images.SyncRequest{SourceRef: args[0]})
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatImageSyncPlan(plan), plan)
			}
			if config.imageManager == nil {
				return fmt.Errorf("image sync executor is not configured")
			}
			result, err := config.imageManager.SyncImage(cmd.Context(), plan)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatImageSyncResult(result), result)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the image sync plan without mutating aliases")
	return command
}

func newAdminImageImportCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var tool string
	var dryRun bool
	command := &cobra.Command{
		Use:   "import base|ai source-ref",
		Short: "Import an OCI image into Incus and set the Sandcastle alias",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := images.PlanImport(config.adminConfig, images.ImportRequest{
				Template:  args[0],
				SourceRef: args[1],
				Tool:      tool,
			})
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatImageImportPlan(plan), plan)
			}
			if config.imageImporter == nil {
				return fmt.Errorf("image import executor is not configured")
			}
			result, err := config.imageImporter.ImportImage(cmd.Context(), plan)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatImageImportResult(result), result)
		},
	}
	command.Flags().StringVar(&tool, "tool", "incus", "Incus CLI executable")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the image import command without running it")
	return command
}

func formatImageBuildPlan(plan images.BuildPlan) string {
	return fmt.Sprintf("Image: %s\nTemplate: %s\nCommand: %s", plan.Tag, plan.Template, strings.Join(plan.Command, " "))
}

func formatImageBuildResult(result images.BuildResult) string {
	return fmt.Sprintf("Image: %s\nTemplate: %s\nBuilt: %t", result.Tag, result.Template, result.Built)
}

func formatImageImportPlan(plan images.ImportPlan) string {
	return fmt.Sprintf("Import: %s\nTemplate: %s\nAlias: %s\nCommand: %s", plan.SourceRef, plan.Template, plan.Alias, strings.Join(plan.Command, " "))
}

func formatImageImportResult(result images.ImportResult) string {
	return fmt.Sprintf("Import: %s\nAlias: %s\nImported: %t", result.SourceRef, result.Alias, result.Imported)
}

func formatImageSyncPlan(plan images.SyncPlan) string {
	return fmt.Sprintf("Image: %s\nTemplate: %s\nAlias: %s", plan.SourceRef, plan.Template, plan.Alias)
}

func formatImageSyncResult(result images.SyncResult) string {
	return fmt.Sprintf("Image: %s\nAlias: %s\nFingerprint: %s\nAction: %s", result.SourceRef, result.Alias, result.Fingerprint, result.Action)
}

func newAdminTLDCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	tldCommand := &cobra.Command{
		Use:   "tld",
		Short: "Manage tenant suffix deny-list snapshots",
	}
	tldCommand.AddCommand(newAdminTLDRefreshCommand(config, opts))
	return tldCommand
}

func newAdminTLDRefreshCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var sourceURL string
	var outputPath string
	var specialUseSourceURL string
	var specialUseOutputPath string
	var dryRun bool
	command := &cobra.Command{
		Use:   "refresh",
		Short: "Refresh embedded public TLD and special-use deny-list snapshots",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := domain.RefreshDenyListSnapshots(cmd.Context(), nil, domain.DenyListRefreshRequest{
				TLDSourceURL:         sourceURL,
				TLDOutputPath:        outputPath,
				SpecialUseSourceURL:  specialUseSourceURL,
				SpecialUseOutputPath: specialUseOutputPath,
				DryRun:               dryRun,
			})
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatTLDRefreshResult(result), result)
		},
	}
	command.Flags().StringVar(&sourceURL, "source-url", domain.IANAAlphaTLDURL, "IANA alpha TLD list URL")
	command.Flags().StringVar(&outputPath, "output-file", domain.DefaultTLDSnapshotOutput, "generated Go source output path")
	command.Flags().StringVar(&specialUseSourceURL, "special-use-source-url", domain.IANASpecialUseDomainCSVURL, "IANA special-use domain CSV URL")
	command.Flags().StringVar(&specialUseOutputPath, "special-use-output-file", domain.DefaultSpecialUseSnapshotOutputPath, "generated special-use Go source output path")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "fetch and validate without writing the generated snapshot")
	return command
}

func formatTLDRefreshResult(result domain.DenyListRefreshResult) string {
	if result.TLD.Written || result.SpecialUse.Written {
		return fmt.Sprintf(
			"Refreshed %d public TLDs from %s into %s and %d special-use domains from %s into %s",
			result.TLD.Count,
			result.TLD.SourceURL,
			result.TLD.OutputPath,
			result.SpecialUse.Count,
			result.SpecialUse.SourceURL,
			result.SpecialUse.OutputPath,
		)
	}
	return fmt.Sprintf(
		"Validated %d public TLDs from %s and %d special-use domains from %s; %s and %s were not written",
		result.TLD.Count,
		result.TLD.SourceURL,
		result.SpecialUse.Count,
		result.SpecialUse.SourceURL,
		result.TLD.OutputPath,
		result.SpecialUse.OutputPath,
	)
}

func newAdminUserCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	user := &cobra.Command{
		Use:   "user",
		Short: "Manage Sandcastle restricted users",
	}
	user.AddCommand(newAdminUserCreateCommand(config, opts))
	user.AddCommand(newAdminUserDeleteCommand(config, opts))
	user.AddCommand(newAdminUserTokenCommand(config, opts))
	return user
}

func newAdminUserCreateCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	var tenants []string
	command := &cobra.Command{
		Use:   "create user",
		Short: "Create a restricted Sandcastle user certificate token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := planUserToken(config, args[0], tenants)
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
				return writeOutput(config.stdout, opts.output, formatTokenResult(config, result), result)
			}
			return writeOutput(config.stdout, opts.output, formatUserPlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the restricted user plan without mutating trust state")
	command.Flags().StringSliceVar(&tenants, "tenant", nil, "tenant to pre-grant in the generated certificate token (repeatable)")
	return command
}

func newAdminUserDeleteCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "delete user",
		Short: "Delete a restricted Sandcastle user certificate",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := usertrust.PlanDeleteUser(args[0])
			if err != nil {
				return err
			}
			if !dryRun {
				if config.trustManager == nil {
					return fmt.Errorf("restricted user delete executor is not configured")
				}
				if err := config.trustManager.Delete(cmd.Context(), plan); err != nil {
					return err
				}
				return writeOutput(config.stdout, opts.output, formatUserDelete(plan), plan)
			}
			return writeOutput(config.stdout, opts.output, formatUserPlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the delete plan without deleting a certificate")
	return command
}

func newAdminUserTokenCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	var tenants []string
	command := &cobra.Command{
		Use:   "token user",
		Short: "Create a restricted certificate add token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := planUserToken(config, args[0], tenants)
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
				return writeOutput(config.stdout, opts.output, formatTokenResult(config, result), result)
			}
			return writeOutput(config.stdout, opts.output, formatUserPlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the token plan without creating a trust token")
	command.Flags().StringSliceVar(&tenants, "tenant", nil, "tenant to pre-grant in the generated certificate token (repeatable)")
	return command
}

func planUserToken(config commandConfig, user string, tenants []string) (usertrust.UserPlan, error) {
	if len(tenants) > 0 {
		return usertrust.PlanGrant(config.adminConfig, usertrust.GrantRequest{
			User:     user,
			Projects: tenants,
		})
	}
	return usertrust.PlanToken(user)
}

func formatUserPlan(plan usertrust.UserPlan) string {
	projects := "none"
	if len(plan.Projects) > 0 {
		projects = strings.Join(plan.Projects, ", ")
	}
	return fmt.Sprintf("User: %s\nCertificate: %s\nRemote: %s\nRestricted: %t\nProjects: %s", plan.User, plan.CertificateName, plan.RemoteName, plan.Restricted, projects)
}

func formatUserDelete(plan usertrust.UserPlan) string {
	return fmt.Sprintf("Deleted restricted user certificate: %s", plan.CertificateName)
}

func formatTokenResult(config commandConfig, result usertrust.TokenResult) string {
	remoteName := result.RemoteName
	if remoteName == "" {
		remoteName = usertrust.RestrictedName(result.User)
	}
	bootstrap := fmt.Sprintf("sc remote add %s %s", remoteName, result.Token)
	tenant := bootstrapTenant(config, result)
	if tenant != "" {
		bootstrap += " --tenant " + tenant
	}
	output := fmt.Sprintf(
		"User: %s\nCertificate: %s\nRemote: %s\nToken: %s\nBootstrap:\n  %s",
		result.User,
		result.CertificateName,
		remoteName,
		result.Token,
		bootstrap,
	)
	if tenant == "" {
		output += "\nAfter tenant access is granted, set the default tenant with:\n  sc config set tenant <tenant>"
	}
	return output
}

func bootstrapTenant(config commandConfig, result usertrust.TokenResult) string {
	if len(result.Projects) == 0 {
		return ""
	}
	prefix := config.adminConfig.IncusProjectPrefix
	if prefix == "" {
		prefix = "sc"
	}
	if tenant, ok := strings.CutPrefix(result.Projects[0], prefix+"-"); ok {
		return tenant
	}
	return ""
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

func newAdminAuthAppCommand(config commandConfig) *cobra.Command {
	auth := &cobra.Command{
		Use:   "auth-app",
		Short: "Manage the Sandcastle Auth App",
	}
	auth.AddCommand(newAdminAuthAppServeCommand(config))
	return auth
}

func newAdminAuthAppServeCommand(config commandConfig) *cobra.Command {
	var listen string
	var databasePath string
	var authHostname string
	var githubClientID string
	var githubClientSecret string
	var adminGitHubUsers string
	var debugDeviceUser string
	var defaultUnixUser string
	var tailscaleAuthKey string
	command := &cobra.Command{
		Use:   "serve",
		Short: "Serve the Sandcastle Auth App",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := authapp.PlanServe(authapp.ServeRequest{
				Address:             listen,
				DatabasePath:        databasePath,
				AuthHostname:        authHostname,
				GitHubClientID:      githubClientID,
				GitHubClientSecret:  githubClientSecret,
				BootstrapAdminUsers: strings.Split(adminGitHubUsers, ","),
				DebugDeviceUser:     debugDeviceUser,
				DefaultUnixUser:     defaultUnixUser,
				TailscaleAuthKey:    tailscaleAuthKey,
			})
			if err != nil {
				return err
			}
			if config.authApp == nil {
				return fmt.Errorf("auth app server is not configured")
			}
			return config.authApp.Serve(cmd.Context(), plan)
		},
	}
	command.Flags().StringVar(&listen, "listen", ":9444", "auth app listen address")
	command.Flags().StringVar(&databasePath, "database", "", "SQLite auth database path")
	command.Flags().StringVar(&authHostname, "auth-hostname", "", "public Auth Hostname")
	command.Flags().StringVar(&githubClientID, "github-client-id", "", "GitHub OAuth client id")
	command.Flags().StringVar(&githubClientSecret, "github-client-secret", "", "GitHub OAuth client secret")
	command.Flags().StringVar(&adminGitHubUsers, "admin-github-users", "", "comma-separated initial Sandcastle Admin GitHub usernames")
	command.Flags().StringVar(&debugDeviceUser, "debug-device-user", "", "enable debug device approval as this allowlisted GitHub username")
	command.Flags().StringVar(&defaultUnixUser, "default-unix-user", "", "default Unix username for newly provisioned Personal Tenant machines")
	command.Flags().StringVar(&tailscaleAuthKey, "tailscale-auth-key", "", "Tailscale auth key returned to approved CLI device logins for unattended tenant attachment")
	_ = command.MarkFlagRequired("database")
	return command
}

func newAdminMachineCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "machine",
		Short: "Manage Sandcastle machines",
	}
	cmd.AddCommand(newAdminMachineWorkloadCommand(config, opts))
	return cmd
}

func newAdminMachineWorkloadCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workload",
		Short: "Manage workload identity for machines",
	}
	cmd.AddCommand(newAdminMachineWorkloadEnableCommand(config, opts))
	return cmd
}

func newAdminMachineWorkloadEnableCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var databasePath string
	var authHostname string
	command := &cobra.Command{
		Use:   "enable [project:]machine",
		Short: "Enable workload identity for a machine",
		Long: `Enable workload identity for a machine.

Registers the machine in the auth app database and writes the runtime secret,
token endpoint, and machine identity files to the machine. The machine can then
exchange the runtime secret for a short-lived JWT at the token endpoint.

From within the machine:
  secret=$(cat /var/lib/sandcastle/workload/runtime-secret)
  endpoint=$(cat /var/lib/sandcastle/workload/token-endpoint)
  tenant=$(cat /var/lib/sandcastle/workload/tenant)
  project=$(cat /var/lib/sandcastle/workload/project)
  machine=$(cat /var/lib/sandcastle/workload/machine)
  audience="//iam.googleapis.com/projects/PROJECT_NUMBER/locations/global/workloadIdentityPools/sandcastle-$tenant/providers/sandcastle"
  curl -s -X POST "$endpoint" \
    -H "Content-Type: application/json" \
    -d "{\"tenant\":\"$tenant\",\"project\":\"$project\",\"machine\":\"$machine\",\"runtime_secret\":\"$secret\",\"audience\":\"$audience\"}"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := strings.TrimSpace(authHostname)
			if host == "" {
				host = config.adminConfig.AuthHostname
			}
			if host == "" {
				return fmt.Errorf("--auth-hostname is required (or set SANDCASTLE_AUTH_HOSTNAME)")
			}
			createTenantStore := tenantStoreWithSSHKeyMetadata(config.tenantStore)
			plan, err := machine.PlanCreate(cmd.Context(), config.adminConfig, createTenantStore, config.machineStore, machine.CreateRequest{
				Reference: args[0],
			})
			if err != nil {
				return err
			}
			db, err := sql.Open("sqlite", databasePath)
			if err != nil {
				return fmt.Errorf("open auth database: %w", err)
			}
			defer db.Close()
			result, err := authapp.EnableMachineWorkloadIdentity(cmd.Context(), db, host, authapp.MachineRuntimeSecretRequest{
				Tenant:         plan.Tenant.Tenant,
				Project:        plan.Project,
				Machine:        plan.Name,
				UserKey:        plan.Tenant.Tenant,
				GitHubUsername: plan.Tenant.Tenant,
			})
			if err != nil {
				return fmt.Errorf("enable workload identity: %w", err)
			}
			plan.WorkloadFiles, err = machine.WorkloadIdentityFiles(&machine.WorkloadIdentityRequest{
				TokenEndpoint: result.TokenEndpoint,
				RuntimeSecret: result.RuntimeSecret,
				Tenant:        plan.Tenant.Tenant,
				Project:       plan.Project,
				Machine:       plan.Name,
			})
			if err != nil {
				return fmt.Errorf("build workload identity files: %w", err)
			}
			plan.CertificateFiles = []machine.File{} // skip cert re-issue; only update workload files
			if config.machineCreator == nil {
				return fmt.Errorf("machine creator is not configured")
			}
			if err := config.machineCreator.CreateMachine(cmd.Context(), plan); err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, fmt.Sprintf(
				"Workload identity enabled for %s/%s/%s\nToken endpoint: %s\nOIDC issuer:    %s",
				plan.Tenant.Tenant, plan.Project, plan.Name,
				result.TokenEndpoint,
				result.Issuer,
			), result)
		},
	}
	command.Flags().StringVar(&databasePath, "database", "", "SQLite auth database path")
	command.Flags().StringVar(&authHostname, "auth-hostname", "", "public Auth Hostname (overrides SANDCASTLE_AUTH_HOSTNAME)")
	_ = command.MarkFlagRequired("database")
	return command
}
