package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/authapp"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/images"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/infra"
	"github.com/thieso2/sandcastle-incus/internal/localtrust"
	"github.com/thieso2/sandcastle-incus/internal/routebroker"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
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
		if globalCfg, err := cliconfig.LoadConfig(""); err == nil {
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
	}

	sharedRemote := incusx.NewSharedRemote(adminConfig.Remote).WithVerbose(verbose, os.Stderr)
	directRouteManager := incusx.NewRouteManager(adminConfig.Remote)
	directRouteManager.InfrastructureProject = adminConfig.InfrastructureProject
	directRouteManager.LetsEncryptEmail = adminConfig.LetsEncryptEmail
	directRouteManager.InfrastructureTLSMode = adminConfig.InfrastructureTLSMode
	routeBrokerTenants := incusx.NewTenantStoreForSharedRemote(sharedRemote)
	routeBrokerMachines := incusx.NewHostOverrideManagerForSharedRemote(sharedRemote)
	routeBrokerTrust := incusx.NewRouteBrokerTrustMapper(adminConfig.Remote)
	authAppTenants := incusx.NewTenantStoreForSharedRemote(sharedRemote)
	authAppMachines := incusx.NewHostOverrideManagerForSharedRemote(sharedRemote)
	authAppCreator := incusx.NewTenantCreator(adminConfig.Remote).WithVerbose(verbose, os.Stderr)
	authAppTrust := incusx.NewTrustManager(adminConfig.Remote)
	authAppSSHKeys := incusx.NewMachineSSHKeyReconciler(adminConfig.Remote, authAppMachines)
	authAppMetadataUpdater := incusx.TenantSSHKeyManager{Remote: adminConfig.Remote}
	authAppShareReconciler := incusx.NewShareReconciler(adminConfig.Remote, authAppMachines)
	authAppShareReconciler.Admin = adminConfig
	var authAppProjectUpdater tenant.ProjectUpdater = authAppMetadataUpdater
	if routeBrokerServeArgs(args) {
		if socketServer, err := routeBrokerSocketServer(); err == nil && socketServer != nil {
			routeBrokerTenants = incusx.NewTenantStoreForServer(socketServer)
			routeBrokerMachines = incusx.NewHostOverrideManagerForServer(socketServer)
			directRouteManager = incusx.NewRouteManagerForServer(socketServer)
			directRouteManager.InfrastructureProject = adminConfig.InfrastructureProject
			directRouteManager.LetsEncryptEmail = adminConfig.LetsEncryptEmail
			directRouteManager.InfrastructureTLSMode = adminConfig.InfrastructureTLSMode
			routeBrokerTrust = incusx.NewRouteBrokerTrustMapperForServer(socketServer)
		} else if err != nil && verbose {
			fmt.Fprintf(os.Stderr, "[verbose] route broker unix socket unavailable: %v\n", err)
		}
	}
	if authAppServeArgs(args) {
		if socketServer, err := routeBrokerSocketServer(); err == nil && socketServer != nil {
			authAppTenants = incusx.NewTenantStoreForServer(socketServer)
			authAppMachines = incusx.NewHostOverrideManagerForServer(socketServer)
			authAppCreator = incusx.NewTenantCreatorForServer(socketServer).WithVerbose(verbose, os.Stderr)
			authAppTrust = incusx.NewTrustManagerForServer(socketServer)
			authAppSSHKeys = incusx.NewMachineSSHKeyReconcilerForServer(socketServer, authAppMachines)
			authAppMetadataUpdater = incusx.NewTenantSSHKeyManagerForServer(socketServer)
			authAppShareReconciler = incusx.NewShareReconcilerForServer(socketServer, authAppMachines, authAppMetadataUpdater, adminConfig)
			authAppProjectUpdater = authAppMetadataUpdater
		} else if err != nil && verbose {
			fmt.Fprintf(os.Stderr, "[verbose] auth app unix socket unavailable: %v\n", err)
		}
	}

	cmd := NewAdminRootCommand(commandConfig{
		name:                name,
		stdin:               os.Stdin,
		stdout:              os.Stdout,
		stderr:              os.Stderr,
		adminConfig:         adminConfig,
		tenantStore:         incusx.NewTenantStoreForSharedRemote(sharedRemote),
		tenantCreator:       incusx.NewTenantCreator(adminConfig.Remote).WithVerbose(verbose, os.Stderr),
		tenantDeleter:       incusx.NewTenantDeleter(adminConfig.Remote).WithVerbose(verbose, os.Stderr),
		tenantSSHKeyUpdater: incusx.NewTenantSSHKeyManager(adminConfig.Remote),
		tenantUpdater:       incusx.NewTenantSSHKeyManager(adminConfig.Remote),
		infraCreator:        incusx.NewInfrastructureCreator(adminConfig.Remote).WithVerbose(verbose, os.Stderr),
		infraDeleter:        incusx.NewInfrastructureDeleter(adminConfig.Remote).WithVerbose(verbose, os.Stderr),
		infraCaddyData:      incusx.NewInfrastructureCaddyDataExporter(adminConfig.Remote).WithVerbose(verbose, os.Stderr),
		imageManager:        incusx.NewImageManager(adminConfig.Remote),
		imageBuilder:        images.LocalBuilder{},
		imageImporter:       images.LocalImporter{},
		imageUploader:       images.LocalUploader{},
		topologyStore:       incusx.NewTopologyStore(adminConfig.Remote),
		trustManager:        incusx.NewTrustManager(adminConfig.Remote),
		localTrust:          incusx.NewLocalTrustManager(adminConfig.Remote, localtrust.NewPlatformStore()),
		machineCreator:      incusx.NewMachineCreator(adminConfig.Remote).WithVerbose(verbose, os.Stderr),
		machineStore:        incusx.NewHostOverrideManagerForSharedRemote(sharedRemote),
		machineConnector:    incusx.NewMachineConnector(adminConfig.Remote).WithVerbose(verbose, os.Stderr),
		machineControl:      incusx.NewMachineController(adminConfig.Remote),
		machinePort:         incusx.NewMachinePortSetter(adminConfig.Remote),
		knownHosts:          newLocalKnownHostsManager(verbose, os.Stderr),
		tailscale:           incusx.NewTailscaleManager(adminConfig.Remote),
		routeBroker: routebroker.HTTPRunner{Server: routebroker.Server{
			Admin:         adminConfig,
			Tenants:       routeBrokerTenants,
			Machines:      routeBrokerMachines,
			Routes:        directRouteManager,
			RouteMetadata: directRouteManager,
			Trust:         routeBrokerTrust,
		}},
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
			Provisioner: authapp.Provisioner{
				Admin:           adminConfig,
				Tenants:         authAppTenants,
				TenantCreator:   authAppCreator,
				ProjectUpdater:  authAppProjectUpdater,
				UnixUserUpdater: authAppMetadataUpdater,
				AuxProjects:     authAppCreator,
				Trust:           authAppTrust,
			},
		},
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

func routeBrokerSocketServer() (incus.InstanceServer, error) {
	if _, err := os.Stat(infra.RouteBrokerIncusSocketPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return incus.ConnectIncusUnix(infra.RouteBrokerIncusSocketPath, nil)
}

func routeBrokerServeArgs(args []string) bool {
	if len(args) < 2 {
		return false
	}
	return args[0] == "route-broker" && args[1] == "serve"
}

func authAppServeArgs(args []string) bool {
	if len(args) < 2 {
		return false
	}
	return args[0] == "auth-app" && args[1] == "serve"
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
	root.AddCommand(newAdminMachineCreateCommand(config, opts))
	root.AddCommand(newAdminMachineConnectCommand(config, opts))
	root.AddCommand(newAdminMachineStatusCommand(config, opts))
	root.AddCommand(newAdminMachineDeleteCommand(config, opts))
	root.AddCommand(newAdminTenantCommand(config, opts))
	root.AddCommand(newAdminUserCommand(config, opts))
	root.AddCommand(newAdminInfraCommand(config, opts))
	root.AddCommand(newAdminImageCommand(config, opts))
	root.AddCommand(newAdminTLDCommand(config, opts))
	root.AddCommand(newAdminRouteBrokerCommand(config))
	root.AddCommand(newAdminAuthAppCommand(config))
	root.AddCommand(newAdminMachineWorkloadCommand(config, opts))
	root.AddCommand(newConfigCommand(config, opts))

	return root
}
