package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/incus/v6/shared/cliconfig"
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
	// Admin commands use the global Incus config (~/.config/incus/) with the admin remote.
	// User-facing commands use the per-remote Sandcastle dir (restricted cert).
	isAdmin := len(args) > 0 && args[0] == "admin"
	verbose := os.Getenv("VERBOSE") == "1"
	if isAdmin {
		// Prefer explicit admin_remote; fall back to auto-detecting the global remote
		// whose server TLS cert matches the per-remote user config.
		adminRemote := adminConfig.AdminRemote
		if adminRemote == "" {
			adminRemote = detectAdminRemote(adminConfig.Remote, verbose)
			if verbose && adminRemote != "" {
				fmt.Fprintf(os.Stderr, "[verbose] admin remote auto-detected: %s\n", adminRemote)
			}
		}
		if adminRemote != "" {
			adminConfig.Remote = adminRemote
		}
		// INCUS_CONF intentionally not set → uses ~/.config/incus/ (admin certs)
	} else {
		if userPath := scconfig.ResolveConfigPath(adminConfig.Remote); userPath != "" {
			os.Setenv("INCUS_CONF", userPath)
		}
	}
	if verbose {
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

// detectAdminRemote finds the global Incus remote (~/.config/incus/) that points to the same
// server as the user's per-remote Sandcastle incus config, by comparing server TLS certificates.
// This is more reliable than address comparison (which can fail when one config uses an IP and
// the other uses a hostname).
func detectAdminRemote(userRemote string, verbose bool) string {
	userDir := scconfig.ResolveConfigPath(userRemote)
	if userDir == "" {
		if verbose {
			fmt.Fprintf(os.Stderr, "[verbose] admin remote detection: no per-remote config dir for %q\n", userRemote)
		}
		return ""
	}

	// Read the server certificate stored in the per-remote config dir.
	userCertPath := filepath.Join(userDir, "servercerts", userRemote+".crt")
	userCert, err := os.ReadFile(userCertPath)
	if err != nil || len(userCert) == 0 {
		if verbose {
			fmt.Fprintf(os.Stderr, "[verbose] admin remote detection: no server cert at %s, trying address match\n", userCertPath)
		}
		return detectAdminRemoteByAddr(userRemote, userDir, verbose)
	}

	// Scan global incus servercerts/ for a matching certificate.
	home, _ := os.UserHomeDir()
	globalCertsDir := filepath.Join(home, ".config", "incus", "servercerts")
	entries, err := os.ReadDir(globalCertsDir)
	if err != nil {
		if verbose {
			fmt.Fprintf(os.Stderr, "[verbose] admin remote detection: cannot read %s: %v\n", globalCertsDir, err)
		}
		return ""
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".crt") {
			continue
		}
		globalCert, err := os.ReadFile(filepath.Join(globalCertsDir, entry.Name()))
		if err != nil {
			continue
		}
		if bytes.Equal(userCert, globalCert) {
			return strings.TrimSuffix(entry.Name(), ".crt")
		}
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "[verbose] admin remote detection: no global remote cert matches %s\n", userCertPath)
	}
	return ""
}

// detectAdminRemoteByAddr is the fallback detection path that compares remote addresses via cliconfig.
func detectAdminRemoteByAddr(userRemote string, userDir string, verbose bool) string {
	userCfg, err := cliconfig.LoadConfig(userDir)
	if err != nil {
		if verbose {
			fmt.Fprintf(os.Stderr, "[verbose] admin remote detection: cannot load user config from %s: %v\n", userDir, err)
		}
		return ""
	}
	userRemoteInfo, ok := userCfg.Remotes[userRemote]
	if !ok {
		if verbose {
			fmt.Fprintf(os.Stderr, "[verbose] admin remote detection: remote %q not found in %s/config.yml\n", userRemote, userDir)
		}
		return ""
	}
	globalCfg, err := cliconfig.LoadConfig("")
	if err != nil {
		return ""
	}
	for name, remote := range globalCfg.Remotes {
		if remote.Addr == userRemoteInfo.Addr {
			if verbose {
				fmt.Fprintf(os.Stderr, "[verbose] admin remote detection: addr match: %s == %s (%s)\n", name, userRemote, remote.Addr)
			}
			return name
		}
	}
	if verbose {
		fmt.Fprintf(os.Stderr, "[verbose] admin remote detection: no addr match for %s (%s)\n", userRemote, userRemoteInfo.Addr)
	}
	return ""
}
