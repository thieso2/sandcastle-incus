package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/authapp"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/images"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/localtrust"
	"github.com/thieso2/sandcastle-incus/internal/svclog"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

// ExecuteAdmin runs the Sandcastle admin CLI and returns a process exit code.
// It always uses the global Incus config (~/.config/incus/) with admin TLS certificates —
// INCUS_CONF is never set so the OS default applies.
func ExecuteAdmin(name string, args []string) int {
	adminConfig := scconfig.LoadAdmin()
	verbose := os.Getenv("VERBOSE") == "1"
	explicitRemote := strings.TrimSpace(os.Getenv("SANDCASTLE_REMOTE")) != "" || strings.TrimSpace(adminConfig.AdminRemote) != ""

	// Prefer explicit admin_remote; fall back to cert/IP-based auto-detection;
	// finally fall back to the global Incus default remote.
	adminRemote := adminConfig.AdminRemote
	if adminRemote == "" && !explicitRemote {
		adminRemote = detectAdminRemote(adminConfig.Remote, verbose)
		if verbose && adminRemote != "" {
			fmt.Fprintf(os.Stderr, "[verbose] admin remote auto-detected: %s\n", adminRemote)
		}
	}
	if adminRemote == "" && !explicitRemote {
		if globalCfg, err := incusx.LoadCLIConfig(""); err == nil {
			adminRemote = globalCfg.DefaultRemote
			if verbose && adminRemote != "" {
				fmt.Fprintf(os.Stderr, "[verbose] admin remote: using global incus default %q\n", adminRemote)
			}
		}
	}
	if adminRemote != "" {
		adminConfig.Remote = adminRemote
	}
	// INCUS_CONF intentionally not set → uses ~/.config/incus/ (admin certs)

	if verbose {
		incusConf := os.Getenv("INCUS_CONF")
		if incusConf == "" {
			incusConf = "~/.config/incus (default)"
		}
		fmt.Fprintf(os.Stderr, "[verbose] incus config: %s\n[verbose] incus remote: %s\n", incusConf, adminConfig.Remote)
		incusx.SetAPITrace(os.Stderr)
	}

	sharedRemote := incusx.NewSharedRemote(adminConfig.Remote).WithVerbose(verbose, os.Stderr)
	authAppTenants := incusx.NewTenantStoreForSharedRemote(sharedRemote)
	authAppMachines := incusx.NewHostOverrideManagerForSharedRemote(sharedRemote)
	authAppCreator := incusx.NewTenantCreator(adminConfig.Remote).WithVerbose(verbose, os.Stderr)
	authAppTrust := incusx.NewTrustManager(adminConfig.Remote)
	authAppSSHKeys := incusx.NewMachineSSHKeyReconciler(adminConfig.Remote, authAppMachines)
	authAppMetadataUpdater := incusx.TenantSSHKeyManager{Remote: adminConfig.Remote}
	authAppShareReconciler := incusx.NewShareReconciler(adminConfig.Remote, authAppMachines)
	authAppShareReconciler.Admin = adminConfig
	adminShareStore := incusx.NewTenantSSHKeyManagerWithPool(adminConfig.Remote, adminConfig.StoragePool)
	adminShareReconciler := incusx.NewShareReconciler(adminConfig.Remote, incusx.NewHostOverrideManagerForSharedRemote(sharedRemote))
	adminShareReconciler.Admin = adminConfig
	var authAppSocketServer incus.InstanceServer
	if authAppServeArgs(args) {
		if socketServer, err := adminSocketServer(); err == nil && socketServer != nil {
			authAppSocketServer = socketServer
			authAppTenants = incusx.NewTenantStoreForServer(socketServer)
			authAppMachines = incusx.NewHostOverrideManagerForServer(socketServer)
			authAppCreator = incusx.NewTenantCreatorForServer(socketServer).WithVerbose(verbose, os.Stderr)
			authAppTrust = incusx.NewTrustManagerForServer(socketServer)
			authAppSSHKeys = incusx.NewMachineSSHKeyReconcilerForServer(socketServer, authAppMachines)
			authAppMetadataUpdater = incusx.NewTenantSSHKeyManagerForServer(socketServer)
			authAppShareReconciler = incusx.NewShareReconcilerForServer(socketServer, authAppMachines, authAppMetadataUpdater, adminConfig)
			adminShareStore = authAppMetadataUpdater
			adminShareReconciler = incusx.NewShareReconcilerForServer(socketServer, incusx.NewHostOverrideManagerForServer(socketServer), adminShareStore, adminConfig)
		} else if err != nil && verbose {
			fmt.Fprintf(os.Stderr, "[verbose] auth app unix socket unavailable: %v\n", err)
		}
	}

	// Public Routes (Spec #111) are available only on installs with ACME public
	// ingress (Caddy binds host :80/:443). Wire the Incus-backed RouteBackend and
	// a local Caddy controller only then; otherwise `sc route` returns a clear
	// "no public ingress" error.
	var authAppRoutes authapp.RouteBackend
	var authAppRouteCaddy authapp.CaddyController
	if authAppSocketServer != nil && strings.EqualFold(strings.TrimSpace(os.Getenv("SANDCASTLE_AUTH_INGRESS_MODE")), incusx.IngressACME) {
		v2Prefix := installV2Prefix(adminConfig.IncusProjectPrefix)
		authAppRoutes = incusx.RouteBackend{
			Server:          authAppSocketServer,
			MachinePrefix:   adminConfig.IncusProjectPrefix,
			AuthAppInstance: v2Prefix + "-auth-app",
			AuthAppProject:  v2Prefix + "-infra",
		}
		authAppRouteCaddy = authapp.LocalCaddyController{}
	}

	cmd := NewAdminRootCommand(commandConfig{
		name:               name,
		stdin:              os.Stdin,
		stdout:             os.Stdout,
		stderr:             os.Stderr,
		adminConfig:        adminConfig,
		tenantStore:        incusx.NewTenantStoreForSharedRemote(sharedRemote),
		tenantCreator:      incusx.NewTenantCreator(adminConfig.Remote).WithVerbose(verbose, os.Stderr),
		projectSettings:    incusx.NewTenantCreator(adminConfig.Remote).WithVerbose(verbose, os.Stderr),
		tenantDeleter:      incusx.NewTenantDeleter(adminConfig.Remote).WithVerbose(verbose, os.Stderr),
		projectDeleter:     incusx.NewTenantDeleter(adminConfig.Remote).WithVerbose(verbose, os.Stderr),
		imageManager:       incusx.NewImageManager(adminConfig.Remote),
		imageBuilder:       images.LocalBuilder{},
		imageImporter:      images.LocalImporter{},
		imageUploader:      images.LocalUploader{},
		remoteImageBuilder: images.LocalRemoteBuilder{Token: ghcrTokenFromEnv, Stderr: os.Stderr, Verbose: verbose},
		topologyStore:      incusx.NewTopologyStore(adminConfig.Remote),
		trustManager:       incusx.NewTrustManager(adminConfig.Remote),
		localTrust:         incusx.NewLocalTrustManager(adminConfig.Remote, localtrust.NewPlatformStore()),
		machineStore:       incusx.NewHostOverrideManagerForSharedRemote(sharedRemote),
		tailscale:          incusx.NewTailscaleManager(adminConfig.Remote),
		authApp: authapp.HTTPRunner{
			RestrictedUsers:  authAppTrust,
			Admin:            adminConfig,
			Tenants:          authAppTenants,
			TenantAccess:     authAppTrust,
			Machines:         authAppMachines,
			MachineSSHKeys:   authAppSSHKeys,
			TenantSSHKeys:    authAppMetadataUpdater,
			MachineSSHAccess: authAppSSHKeys,
			ShareStore:       authAppMetadataUpdater,
			ShareReconciler:  authAppShareReconciler,
			DNSReconcile: func(ctx context.Context) error {
				if authAppSocketServer == nil {
					return nil // no mounted socket (not the serving appliance) — nothing to reconcile
				}
				return authAppDNSReconciler(authAppSocketServer, authAppTenants, adminConfig.IncusProjectPrefix).Reconcile(ctx)
			},
			Projects: incusx.ProjectBrokerCreator{
				Creator: authAppCreator,
				Trust:   authAppTrust,
				Prefix:  adminConfig.IncusProjectPrefix,
			},
			DNSEvents: func(ctx context.Context, notify func()) {
				if authAppSocketServer == nil {
					return
				}
				subscribeInstanceLifecycleEvents(ctx, authAppSocketServer, notify)
			},
			Routes:     authAppRoutes,
			RouteCaddy: authAppRouteCaddy,
			ACMEEmail:  strings.TrimSpace(os.Getenv("SANDCASTLE_AUTH_ACME_EMAIL")),
			RouteEvents: func(ctx context.Context, notify func()) {
				if authAppSocketServer == nil {
					return
				}
				subscribeInstanceLifecycleEvents(ctx, authAppSocketServer, notify)
			},
			Provisioner: authapp.Provisioner{
				Admin:   adminConfig,
				Tenants: authAppTenants,
				Trust:   authAppTrust,
				// Login provisioning creates the tenant's default project +
				// sidecar directly over the mounted host socket (the auth app
				// has it, like the broker).
				V2Create: authAppV2Create(adminConfig, authAppCreator),
			},
		},
		shareStore:      adminShareStore,
		shareReconciler: adminShareReconciler,
	})
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func authAppServeArgs(args []string) bool {
	if len(args) < 2 {
		return false
	}
	return args[0] == "auth-app" && args[1] == "serve"
}

// authAppV2Create returns the login-provisioning closure. The closure creates
// the tenant's default project + sidecar directly over the mounted host socket;
// the sidecar image comes from the plan (SANDCASTLE_BASE_IMAGE).
func authAppV2Create(admin scconfig.Admin, creator incusx.TenantCreator) func(context.Context, tenant.CreatePlanV2, string) (authapp.V2CreateResult, error) {
	return func(ctx context.Context, plan tenant.CreatePlanV2, tailscaleAuthKey string) (authapp.V2CreateResult, error) {
		// The tenant's own key wins (BYO tailnet); the service-level key is an
		// optional default for single-user deployments. Both empty is fine —
		// the sidecar starts an interactive join and the login URL flows back.
		key := strings.TrimSpace(tailscaleAuthKey)
		if key == "" {
			key = strings.TrimSpace(admin.AuthTailscaleAuthKey)
		}
		// Name the sidecar's tailnet device after the install + tenant
		// (sc-<install>-<tenant>), so it is globally unique on the tailnet even
		// though the Incus instance is a plain project-scoped "sidecar". The
		// install label comes from the Auth Hostname (the same URL the client's
		// Incus remote is named after); empty auth hostname → the creator falls
		// back to the infra project name.
		var tailnetHostname string
		if remote := usertrust.RemoteNameForAuthHostname(admin.AuthHostname); remote != "" {
			tailnetHostname = remote + "-" + plan.Tenant
		}
		// Stream the tenant bring-up's per-phase progress into svclog so it lands
		// in the login's /logs view (ctx carries the request record from the
		// device poll). Each c.log phase becomes a message entry for this user.
		c := creator
		c.Log = func(msg string) { svclog.Logf(ctx, "%s", msg) }
		var result authapp.V2CreateResult
		err := c.CreateTenantV2(ctx, plan, incusx.CreateV2Options{
			TailscaleAuthKey:       key,
			SidecarTailnetHostname: tailnetHostname,
			OnSidecarTailnetIP:     func(ip string) { result.SidecarTailnetIP = ip },
			OnTailscaleLoginURL:    func(url string) { result.TailscaleLoginURL = url },
		})
		return result, err
	}
}

// NewAdminRootCommand builds the Sandcastle admin command tree with all admin
// subcommands promoted to the top level (no "admin" prefix).
func NewAdminRootCommand(config commandConfig) *cobra.Command {
	if config.name == "" {
		config.name = "sandcastle-admin"
	}
	if config.stdout == nil {
		config.stdout = io.Discard
	}
	if config.stdin == nil {
		config.stdin = strings.NewReader("")
	}
	if config.stderr == nil {
		config.stderr = io.Discard
	}
	if config.adminConfig.Remote == "" {
		config.adminConfig = scconfig.LoadAdmin()
	}
	if config.tenantStore == nil {
		config.tenantStore = tenant.MemoryStore{}
	}

	opts := &rootOptions{output: outputText}
	var jsonOutput bool
	root := &cobra.Command{
		Use:           config.name,
		Short:         "Manage Sandcastle shared infrastructure and user accounts",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if !jsonOutput {
				return nil
			}
			if cmd.Root().PersistentFlags().Changed("output") && opts.output != outputJSON {
				return fmt.Errorf("--json cannot be combined with --output %s", opts.output)
			}
			opts.output = outputJSON
			return nil
		},
	}
	root.PersistentFlags().Var(&opts.output, "output", "output format: text or json")
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "write JSON output")

	root.AddCommand(newAdminVersionCommand(config, opts))
	root.AddCommand(newAdminMachineListCommand(config, opts))
	root.AddCommand(newAdminTenantCommand(config, opts))
	root.AddCommand(newAdminUserCommand(config, opts))
	root.AddCommand(newAdminImageCommand(config, opts))
	root.AddCommand(newAdminTLDCommand(config, opts))
	root.AddCommand(newAdminProjectCommand(config, opts))
	root.AddCommand(newAdminBootstrapCommand(config))
	root.AddCommand(newAdminInstallCommand(config))
	root.AddCommand(newAdminInstallIncusCommand(config))
	root.AddCommand(newAdminAuthAppCommand(config))
	root.AddCommand(newAdminMachineWorkloadCommand(config, opts))
	root.AddCommand(newConfigCommand(config, opts))
	root.AddCommand(newSidecarCommand())

	return root
}

// adminSocketServer connects to the local Incus unix socket. It was named
// routeBrokerSocketServer when the v1 route broker was its first caller; the
// auth-app serve path is now the only one.
func adminSocketServer() (incus.InstanceServer, error) {
	if _, err := os.Stat("/var/lib/incus/unix.socket"); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	server, err := incus.ConnectIncusUnix("/var/lib/incus/unix.socket", nil)
	if err != nil {
		return nil, err
	}
	return incusx.TraceInstanceServer(server), nil
}
