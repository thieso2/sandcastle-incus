package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/authapp"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	"github.com/thieso2/sandcastle-incus/internal/hostoverride"
	"github.com/thieso2/sandcastle-incus/internal/images"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/localdns"
	"github.com/thieso2/sandcastle-incus/internal/localtrust"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/projectbroker"
	"github.com/thieso2/sandcastle-incus/internal/route"
	"github.com/thieso2/sandcastle-incus/internal/routebroker"
	"github.com/thieso2/sandcastle-incus/internal/share"
	"github.com/thieso2/sandcastle-incus/internal/tailscale"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

const version = "0.0.0-dev"

type outputFormat string

const (
	outputText outputFormat = "text"
	outputJSON outputFormat = "json"
)

type commandConfig struct {
	name                 string
	stdin                io.Reader
	stdout               io.Writer
	stderr               io.Writer
	stdinIsTerminal      func(io.Reader) bool
	tenantStore          tenant.IncusTenantStore
	adminConfig          scconfig.Admin
	tenantCreator        incusx.TenantCreator
	tenantDeleter        tenant.Deleter
	tenantSSHKeyUpdater  tenant.SSHKeyUpdater
	tenantUpdater        tenant.ProjectUpdater
	tenantUnixUser       tenant.UnixUserUpdater
	imageManager         images.Manager
	imageBuilder         images.Builder
	imageImporter        images.Importer
	imageUploader        images.Uploader
	remoteImageBuilder   images.RemoteImageBuilder
	topologyStore        tenant.TopologyStore
	trustManager         usertrust.Manager
	machineCreator       machine.Creator
	machineStore         machine.Store
	machineConnector     machine.Connector
	machineControl       machine.Controller
	machinePort          machine.PortSetter
	passwordReconciler   machine.PasswordReconciler
	knownHosts           machineKnownHostsManager
	dnsApplier           dns.Applier
	localDNS             localdns.Manager
	tailscale            tailscale.Runner
	hostOverrides        hostoverride.Manager
	hostMachine          hostoverride.MachineStore
	hostFiles            hostoverride.HostsManager
	localTrust           localtrust.Manager
	routes               route.Manager
	routeMachine         route.MachineStore
	routeBroker          routebroker.Runner
	authApp              authapp.Runner
	authDevice           authDeviceClient
	authWorkload         authWorkloadClient
	authCloudIdentity    authCloudIdentityClient
	authTenants          authTenantClient
	authProjects         authProjectClient
	authShares           authShareClient
	shareStore           share.Store
	shareReconciler      tenantShareReconciler
	openBrowser          func(string)
	loginRemote          loginRemoteInstaller
	loginTailnet         loginTailnetVerifier
	loginSetup           loginSetupRunner
	loginRemoteProbe     func(context.Context, string) error
	loginTailnetPrecheck func(context.Context) error
	loginRoutingCheck    func(context.Context, io.Writer, string) error
	incusRunner          incusRunner
	gcloudRunner         gcloudRunner
}

type tenantShareReconciler interface {
	ReconcileTenantShares(context.Context, tenant.Summary, bool) (share.ReconcileResult, error)
}

type authDeviceClient interface {
	Start(context.Context) (authapp.DeviceStartResult, error)
	Poll(context.Context, string, authapp.DevicePollRequest) (authapp.DevicePollResult, error)
	DebugApprove(context.Context, string) error
	SimulateApprove(ctx context.Context, userCode, username, token string) error
}

type authWorkloadClient interface {
	Start(context.Context) (authapp.DeviceStartResult, error)
	Poll(context.Context, string, authapp.DevicePollRequest) (authapp.DevicePollResult, error)
	DebugApprove(context.Context, string) error
	EnableWorkload(context.Context, authapp.WorkloadEnableRequest) (authapp.WorkloadEnableResult, error)
}

type authCloudIdentityClient interface {
	UpsertCloudIdentity(context.Context, authapp.CloudIdentityUpsertRequest) (authapp.CloudIdentityConfig, error)
	GetCloudIdentity(context.Context, string, string) (authapp.CloudIdentityConfig, error)
}

type authTenantClient interface {
	ListTenants(context.Context) ([]authapp.TenantAccessSummary, error)
}

type authProjectClient interface {
	CreateProject(context.Context, string) (projectbroker.ProjectResult, error)
}

type authShareClient interface {
	CreateShare(context.Context, authapp.ShareCreateRequest) (share.Result, error)
	ListShares(context.Context, string) ([]meta.TenantStorageShare, error)
	ListInboundShares(context.Context, string) ([]meta.TenantStorageShare, error)
	ListShareOffers(context.Context, string) ([]meta.TenantStorageShare, error)
	GetShare(context.Context, authapp.ShareStatusRequest) (share.Result, error)
	AcceptShare(context.Context, authapp.ShareRecipientRequest) (share.Result, error)
	DeclineShare(context.Context, authapp.ShareRecipientRequest) (share.Result, error)
	RevokeShare(context.Context, authapp.ShareRevokeRequest) (share.Result, error)
	DeleteShare(context.Context, authapp.ShareDeleteRequest) (share.Result, error)
	ReconcileShares(context.Context, authapp.ShareReconcileRequest) (share.ReconcileResult, error)
}

type rootOptions struct {
	output outputFormat
}

// Execute runs the Sandcastle user CLI and returns a process exit code.
// It uses the per-remote Sandcastle Incus config directory (restricted TLS certificate).
// For admin operations use ExecuteAdmin (sandcastle-admin binary).
func Execute(name string, args []string) int {
	adminConfig := scconfig.LoadUser()
	verbose := os.Getenv("VERBOSE") == "1"

	// Always use the per-remote Sandcastle config dir (restricted cert) for user commands.
	if userPath := scconfig.ResolveConfigPath(adminConfig.Remote); userPath != "" {
		os.Setenv("INCUS_CONF", userPath)
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
	directRouteManager.InfrastructureTLSMode = adminConfig.InfrastructureTLSMode
	connectCache := incusx.NewConnectCache(adminConfig.Remote)
	userRouteManager := routeManagerFromEnv()
	sharedRemote := incusx.NewSharedRemote(adminConfig.Remote).WithVerbose(verbose, os.Stderr)
	cmd := NewRootCommand(commandConfig{
		name:                name,
		stdin:               os.Stdin,
		stdout:              os.Stdout,
		stderr:              os.Stderr,
		adminConfig:         adminConfig,
		tenantStore:         incusx.NewTenantStoreForSharedRemote(sharedRemote),
		tenantCreator:       incusx.NewTenantCreator(adminConfig.Remote).WithVerbose(os.Getenv("VERBOSE") == "1", os.Stderr),
		tenantDeleter:       incusx.NewTenantDeleter(adminConfig.Remote).WithVerbose(os.Getenv("VERBOSE") == "1", os.Stderr),
		tenantSSHKeyUpdater: incusx.NewTenantSSHKeyManager(adminConfig.Remote),
		tenantUpdater:       incusx.NewTenantSSHKeyManager(adminConfig.Remote),
		tenantUnixUser:      incusx.NewTenantSSHKeyManager(adminConfig.Remote),
		imageManager:        incusx.NewImageManager(adminConfig.Remote),
		imageBuilder:        images.LocalBuilder{},
		imageImporter:       images.LocalImporter{},
		imageUploader:       images.LocalUploader{},
		remoteImageBuilder:  images.LocalRemoteBuilder{Token: ghcrTokenFromEnv, Stderr: os.Stderr, Verbose: os.Getenv("VERBOSE") == "1"},
		topologyStore:       incusx.NewTopologyStore(adminConfig.Remote),
		trustManager:        incusx.NewTrustManager(adminConfig.Remote),
		machineCreator:      incusx.NewMachineCreator(adminConfig.Remote).WithVerbose(os.Getenv("VERBOSE") == "1", os.Stderr),
		machineStore:        incusx.NewHostOverrideManagerForSharedRemote(sharedRemote),
		machineConnector:    incusx.NewMachineConnector(adminConfig.Remote).WithVerbose(verbose, os.Stderr).WithConnectCache(connectCache),
		machineControl:      incusx.NewMachineController(adminConfig.Remote),
		machinePort:         incusx.NewMachinePortSetter(adminConfig.Remote),
		passwordReconciler:  incusx.NewMachinePasswordReconciler(adminConfig.Remote, incusx.NewHostOverrideManagerForSharedRemote(sharedRemote)),
		knownHosts:          newLocalKnownHostsManager(adminConfig.Remote, verbose, os.Stderr).WithConnectCache(connectCache),
		dnsApplier:          incusx.NewDNSManager(adminConfig.Remote),
		localDNS:            localdns.FileManager{},
		tailscale:           incusx.NewTailscaleManager(adminConfig.Remote),
		hostOverrides:       incusx.NewHostOverrideManagerForSharedRemote(sharedRemote),
		hostMachine:         incusx.NewHostOverrideManagerForSharedRemote(sharedRemote),
		hostFiles:           hostoverride.NewFileHostsManager(os.Getenv("SANDCASTLE_HOSTS_FILE")),
		localTrust:          incusx.NewLocalTrustManager(adminConfig.Remote, localtrust.NewPlatformStore()),
		routes:              userRouteManager,
		routeMachine:        incusx.NewHostOverrideManagerForSharedRemote(sharedRemote),
		routeBroker: routebroker.HTTPRunner{Server: routebroker.Server{
			Admin:         adminConfig,
			Tenants:       incusx.NewTenantStoreForSharedRemote(sharedRemote),
			Machines:      incusx.NewHostOverrideManagerForSharedRemote(sharedRemote),
			Routes:        directRouteManager,
			RouteMetadata: directRouteManager,
			Trust:         incusx.NewRouteBrokerTrustMapper(adminConfig.Remote),
		}},
		openBrowser: openBrowser,
		loginSetup: realLoginSetupRunner{config: commandConfig{
			stdin:       os.Stdin,
			stdout:      os.Stdout,
			stderr:      os.Stderr,
			adminConfig: adminConfig,
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
	if config.tenantStore == nil {
		config.tenantStore = tenant.MemoryStore{}
	}
	if config.adminConfig.Remote == "" {
		config.adminConfig = scconfig.LoadUser()
	}

	opts := &rootOptions{output: outputText}
	var jsonOutput bool
	root := &cobra.Command{
		Use:           config.name,
		Short:         "Manage Incus-backed Sandcastle development machines",
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
	root.AddCommand(newCreateCommand(config, opts))
	root.AddCommand(newImageCommand(config, opts))
	root.AddCommand(newConnectCommand(config, opts))
	root.AddCommand(newConnectV2Command(config, opts))
	root.AddCommand(newMachineLifecycleCommand(config, opts, "start", machine.ActionStart, false))
	root.AddCommand(newMachineLifecycleCommand(config, opts, "stop", machine.ActionStop, false))
	root.AddCommand(newMachineLifecycleCommand(config, opts, "restart", machine.ActionRestart, false))
	root.AddCommand(newMachineLifecycleCommand(config, opts, "delete", machine.ActionDelete, true))
	root.AddCommand(newProjectCommand(config, opts))
	root.AddCommand(newDNSCommand(config, opts))
	root.AddCommand(newDNSProxyCommand(config, opts))
	root.AddCommand(newTailscaleCommand(config, opts))
	root.AddCommand(newTrustCommand(config, opts))
	root.AddCommand(newRemoteCommand(config, opts))
	root.AddCommand(newIncusCommand(config, opts))
	root.AddCommand(newIncusNativeCommand(config, opts))
	root.AddCommand(newIncusInfraCommand(config, opts))
	root.AddCommand(newLoginCommand(config, opts))
	root.AddCommand(newConfigCommand(config, opts))
	root.AddCommand(newTenantCommand(config, opts))
	root.AddCommand(newCloudIdentityCommand(config, opts))
	root.AddCommand(newWorkloadCommand(config, opts))
	root.AddCommand(newShareCommand(config, opts))

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

	// Load global config to know which remotes actually exist (cert files can be stale).
	globalCfg, err := incusx.LoadCLIConfig("")
	if err != nil {
		if verbose {
			fmt.Fprintf(os.Stderr, "[verbose] admin remote detection: cannot load global incus config: %v\n", err)
		}
		return ""
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
		remoteName := strings.TrimSuffix(entry.Name(), ".crt")
		// Skip stale cert files that have no corresponding remote in the config.
		if _, ok := globalCfg.Remotes[remoteName]; !ok {
			continue
		}
		globalCert, err := os.ReadFile(filepath.Join(globalCertsDir, entry.Name()))
		if err != nil {
			continue
		}
		if bytes.Equal(userCert, globalCert) {
			return remoteName
		}
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "[verbose] admin remote detection: no active global remote cert matches %s, trying IP address match\n", userCertPath)
	}
	// No active remote has a cert file that matches — fall back to DNS-based IP matching.
	// (This happens when a remote was renamed: the old cert file is orphaned but the server is the same.)
	return detectAdminRemoteByAddr(userRemote, userDir, verbose)
}

// detectAdminRemoteByAddr matches the per-remote user config's server against global config
// remotes using DNS resolution so hostname vs IP differences don't cause mismatches.
func detectAdminRemoteByAddr(userRemote string, userDir string, verbose bool) string {
	userCfg, err := incusx.LoadCLIConfig(filepath.Join(userDir, "config.yml"))
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
	userHost, userPort := addrHostPort(userRemoteInfo.Addr)
	userIPs := resolveToIPs(userHost)
	if verbose {
		fmt.Fprintf(os.Stderr, "[verbose] admin remote detection: user remote %s addr=%s resolved=%v\n", userRemote, userRemoteInfo.Addr, userIPs)
	}

	globalCfg, err := incusx.LoadCLIConfig("")
	if err != nil {
		return ""
	}
	for name, remote := range globalCfg.Remotes {
		if strings.HasPrefix(remote.Addr, "unix:") || remote.Public {
			continue
		}
		globalHost, globalPort := addrHostPort(remote.Addr)
		if globalPort != userPort {
			continue
		}
		globalIPs := resolveToIPs(globalHost)
		for _, ip := range globalIPs {
			for _, uip := range userIPs {
				if ip == uip {
					if verbose {
						fmt.Fprintf(os.Stderr, "[verbose] admin remote detection: IP match: %s (%s) == %s (%s)\n", name, ip, userRemote, uip)
					}
					return name
				}
			}
		}
	}
	if verbose {
		fmt.Fprintf(os.Stderr, "[verbose] admin remote detection: no IP match for %s (IPs: %v)\n", userRemote, userIPs)
	}
	return ""
}

// addrHostPort extracts the host and port from a remote address like https://host:8443.
func addrHostPort(addr string) (host, port string) {
	u, err := url.Parse(addr)
	if err != nil {
		return addr, ""
	}
	h, p, err := net.SplitHostPort(u.Host)
	if err != nil {
		return u.Host, ""
	}
	return h, p
}

// resolveToIPs resolves a hostname to its IP addresses. If the input is already an IP
// or resolution fails, it returns the input unchanged.
func resolveToIPs(host string) []string {
	if net.ParseIP(host) != nil {
		return []string{host}
	}
	addrs, err := net.LookupHost(host)
	if err != nil || len(addrs) == 0 {
		return []string{host}
	}
	return addrs
}
