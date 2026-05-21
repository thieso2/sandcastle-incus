package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	"github.com/thieso2/sandcastle-incus/internal/hostoverride"
	"github.com/thieso2/sandcastle-incus/internal/images"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/infra"
	"github.com/thieso2/sandcastle-incus/internal/localdns"
	"github.com/thieso2/sandcastle-incus/internal/localtrust"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/route"
	"github.com/thieso2/sandcastle-incus/internal/routebroker"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
	"github.com/thieso2/sandcastle-incus/internal/tailscale"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

const version = "0.0.0-dev"

type outputFormat string

const (
	outputText outputFormat = "text"
	outputJSON outputFormat = "json"
)

type commandConfig struct {
	name            string
	stdin           io.Reader
	stdout          io.Writer
	stderr          io.Writer
	projectStore    project.IncusProjectStore
	adminConfig     scconfig.Admin
	projectCreator      project.Creator
	projectDeleter      project.Deleter
	projectSSHKeyUpdater project.SSHKeyUpdater
	infraCreator    infra.Creator
	infraDeleter    infra.Deleter
	imageManager    images.Manager
	imageBuilder    images.Builder
	imageImporter   images.Importer
	topologyStore   project.TopologyStore
	trustManager    usertrust.Manager
	sandboxCreator  sandbox.Creator
	sandboxStore    sandbox.Store
	sandboxEnterer  sandbox.Enterer
	sandboxControl  sandbox.Controller
	sandboxPort     sandbox.PortSetter
	dnsApplier      dns.Applier
	localDNS        localdns.Manager
	localDNSService localdns.ServiceManager
	tailscale       tailscale.Runner
	hostOverrides   hostoverride.Manager
	hostSandbox     hostoverride.SandboxStore
	hostFiles       hostoverride.HostsManager
	localTrust      localtrust.Manager
	routes          route.Manager
	routeSandbox    route.SandboxStore
	routeBroker     routebroker.Runner
}

type rootOptions struct {
	output outputFormat
}

// Execute runs the Sandcastle CLI and returns a process exit code.
func Execute(name string, args []string) int {
	adminConfig := scconfig.LoadAdmin()
	// Admin commands use the global Incus config (~/.config/incus/) with the admin remote
	// (SANDCASTLE_ADMIN_REMOTE / admin_remote config key). User-facing commands use the
	// per-remote Sandcastle dir (restricted cert) with the user remote.
	isAdmin := len(args) > 0 && args[0] == "admin"
	if isAdmin {
		if adminConfig.AdminRemote != "" {
			adminConfig.Remote = adminConfig.AdminRemote
		}
		// INCUS_CONF intentionally not set → uses ~/.config/incus/ (admin certs)
	} else {
		if userPath := scconfig.ResolveConfigPath(adminConfig.Remote); userPath != "" {
			os.Setenv("INCUS_CONF", userPath)
		}
	}
	if os.Getenv("VERBOSE") == "1" {
		incusConf := os.Getenv("INCUS_CONF")
		if incusConf == "" {
			incusConf = "~/.config/incus (default)"
		}
		fmt.Fprintf(os.Stderr, "[verbose] incus config: %s\n[verbose] incus remote: %s\n", incusConf, adminConfig.Remote)
	}
	directRouteManager := incusx.NewRouteManager(adminConfig.Remote)
	directRouteManager.InfrastructureProject = adminConfig.InfrastructureProject
	directRouteManager.LetsEncryptEmail = adminConfig.LetsEncryptEmail
	userRouteManager := routeManagerFromEnv()
	cmd := NewRootCommand(commandConfig{
		name:        name,
		stdin:       os.Stdin,
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		adminConfig: adminConfig,
		projectStore: incusx.NewProjectStore(
			adminConfig.Remote,
		),
		projectCreator:       incusx.NewProjectCreator(adminConfig.Remote).WithVerbose(os.Getenv("VERBOSE") == "1", os.Stderr),
		projectDeleter:       incusx.NewProjectDeleter(adminConfig.Remote).WithVerbose(os.Getenv("VERBOSE") == "1", os.Stderr),
		projectSSHKeyUpdater: incusx.NewProjectSSHKeyManager(adminConfig.Remote),
		infraCreator:    incusx.NewInfrastructureCreator(adminConfig.Remote),
		infraDeleter:    incusx.NewInfrastructureDeleter(adminConfig.Remote),
		imageManager:    incusx.NewImageManager(adminConfig.Remote),
		imageBuilder:    images.LocalBuilder{},
		imageImporter:   images.LocalImporter{},
		topologyStore:   incusx.NewTopologyStore(adminConfig.Remote),
		trustManager:    incusx.NewTrustManager(adminConfig.Remote),
		sandboxCreator:  incusx.NewSandboxCreator(adminConfig.Remote).WithVerbose(os.Getenv("VERBOSE") == "1", os.Stderr),
		sandboxStore:    incusx.NewHostOverrideManager(adminConfig.Remote),
		sandboxEnterer:  incusx.NewSandboxEnterer(adminConfig.Remote),
		sandboxControl:  incusx.NewSandboxController(adminConfig.Remote),
		sandboxPort:     incusx.NewSandboxPortSetter(adminConfig.Remote),
		dnsApplier:      incusx.NewDNSManager(adminConfig.Remote),
		localDNS:        localdns.FileManager{},
		localDNSService: localdns.FileServiceManager{},
		tailscale:       incusx.NewTailscaleManager(adminConfig.Remote),
		hostOverrides:   incusx.NewHostOverrideManager(adminConfig.Remote),
		hostSandbox:     incusx.NewHostOverrideManager(adminConfig.Remote),
		hostFiles:       hostoverride.NewFileHostsManager(os.Getenv("SANDCASTLE_HOSTS_FILE")),
		localTrust:      incusx.NewLocalTrustManager(adminConfig.Remote, localtrust.NewPlatformStore()),
		routes:          userRouteManager,
		routeSandbox:    incusx.NewHostOverrideManager(adminConfig.Remote),
		routeBroker: routebroker.HTTPRunner{Server: routebroker.Server{
			Admin:         adminConfig,
			Projects:      incusx.NewProjectStore(adminConfig.Remote),
			Sandboxes:     incusx.NewHostOverrideManager(adminConfig.Remote),
			Routes:        directRouteManager,
			RouteMetadata: directRouteManager,
			Trust:         incusx.NewRouteBrokerTrustMapper(adminConfig.Remote),
		}},
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

func routeManagerFromEnv() route.Manager {
	brokerURL := strings.TrimSpace(os.Getenv("SANDCASTLE_ROUTE_BROKER_URL"))
	if brokerURL == "" {
		return nil
	}
	return routebroker.Client{
		BaseURL:            brokerURL,
		CertFile:           strings.TrimSpace(os.Getenv("SANDCASTLE_ROUTE_BROKER_CLIENT_CERT")),
		KeyFile:            strings.TrimSpace(os.Getenv("SANDCASTLE_ROUTE_BROKER_CLIENT_KEY")),
		InsecureSkipVerify: strings.TrimSpace(os.Getenv("SANDCASTLE_ROUTE_BROKER_INSECURE_SKIP_VERIFY")) == "1",
	}
}

// NewRootCommand builds the Sandcastle command tree.
func NewRootCommand(config commandConfig) *cobra.Command {
	if config.name == "" {
		config.name = "sandcastle"
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
	if config.projectStore == nil {
		config.projectStore = project.MemoryStore{}
	}
	if config.adminConfig.Remote == "" {
		config.adminConfig = scconfig.LoadAdmin()
	}

	opts := &rootOptions{output: outputText}
	var jsonOutput bool
	root := &cobra.Command{
		Use:           config.name,
		Short:         "Manage Incus-backed Sandcastle development sandboxes",
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

	root.AddCommand(newVersionCommand(config, opts))
	root.AddCommand(newListCommand(config, opts))
	root.AddCommand(newStatusCommand(config, opts))
	root.AddCommand(newInspectCommand(config, opts))
	root.AddCommand(newAddCommand(config, opts))
	root.AddCommand(newEnterCommand(config, opts))
	root.AddCommand(newSandboxLifecycleCommand(config, opts, "start", sandbox.ActionStart, false))
	root.AddCommand(newSandboxLifecycleCommand(config, opts, "stop", sandbox.ActionStop, false))
	root.AddCommand(newSandboxLifecycleCommand(config, opts, "restart", sandbox.ActionRestart, false))
	root.AddCommand(newSandboxLifecycleCommand(config, opts, "rm", sandbox.ActionRemove, true))
	root.AddCommand(newPortCommand(config, opts))
	root.AddCommand(newDNSCommand(config, opts))
	root.AddCommand(newTailscaleCommand(config, opts))
	root.AddCommand(newHostCommand(config, opts))
	root.AddCommand(newTrustCommand(config, opts))
	root.AddCommand(newRouteCommand(config, opts))
	root.AddCommand(newAdminCommand(config, opts))
	root.AddCommand(newRemoteCommand(config, opts))
	root.AddCommand(newIncusCommand(config, opts))
	root.AddCommand(newConfigCommand(config, opts))

	return root
}

func (f *outputFormat) Set(value string) error {
	switch outputFormat(value) {
	case outputText, outputJSON:
		*f = outputFormat(value)
		return nil
	default:
		return fmt.Errorf("unsupported output format %q", value)
	}
}

func (f outputFormat) String() string {
	if f == "" {
		return string(outputText)
	}
	return string(f)
}

func (f outputFormat) Type() string {
	return "format"
}
