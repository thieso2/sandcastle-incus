package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/authapp"
	"github.com/thieso2/sandcastle-incus/internal/domain"
	"github.com/thieso2/sandcastle-incus/internal/images"
	"github.com/thieso2/sandcastle-incus/internal/infra"
	"github.com/thieso2/sandcastle-incus/internal/routebroker"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
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
		Use:   "list",
		Short: "List Sandcastle tenants",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tenants, err := listTenants(cmd.Context(), config.tenantStore)
			if err != nil {
				return err
			}
			payload := tenantListPayload{Tenants: tenants}
			return writeOutput(config.stdout, opts.output, formatTenantList(tenants), payload)
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
				if err := config.tenantCreator.CreateTenant(cmd.Context(), plan); err != nil {
					return err
				}
			}
			return writeOutput(config.stdout, opts.output, formatCreatePlan(plan), plan)
		},
	}
	command.Flags().StringVar(&sshKey, "ssh-key", "", "SSH public key to inject into all tenant machines")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the Incus creation plan without mutating resources")
	return command
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
				return fmt.Errorf("refusing to delete without --yes")
			}
			plan, err := tenant.PlanDelete(config.adminConfig, tenant.DeleteRequest{
				Reference: args[0],
				Purge:     purge,
			})
			if err != nil {
				return err
			}
			if config.tenantDeleter == nil {
				return fmt.Errorf("tenant deletion executor is not configured")
			}
			if err := config.tenantDeleter.DeleteTenant(cmd.Context(), plan); err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatDeletePlan(plan), plan)
		},
	}
	command.Flags().BoolVar(&yes, "yes", false, "confirm tenant deletion")
	command.Flags().BoolVar(&purge, "purge", false, "delete durable tenant volumes and the Incus project")
	return command
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
	infraCommand.AddCommand(newAdminInfraDeleteCommand(config, opts))
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
				writeInfraConfigBanner(config, opts)
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

func newAdminInfraDeleteCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var yes bool
	command := &cobra.Command{
		Use:   "delete",
		Short: "Delete Sandcastle shared infrastructure",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("refusing to delete infrastructure without --yes")
			}
			plan, err := infra.PlanDelete(config.adminConfig, infra.DeleteRequest{})
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
	command.Flags().BoolVar(&yes, "yes", false, "confirm infrastructure deletion")
	return command
}

func formatInfraDeletePlan(plan infra.DeletePlan) string {
	return fmt.Sprintf("Deleted infrastructure project: %s", plan.Project)
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
  SANDCASTLE_BASE_IMAGE=%s
  SANDCASTLE_AI_IMAGE=%s
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
		bannerValue(admin.Images.Base),
		bannerValue(admin.Images.AI),
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
	_ = command.MarkFlagRequired("database")
	return command
}
