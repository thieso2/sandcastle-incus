package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/authapp"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/localdns"
	"github.com/thieso2/sandcastle-incus/internal/localtrust"
	"github.com/thieso2/sandcastle-incus/internal/tailscale"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

type loginRemoteInstallRequest struct {
	RemoteName   string
	Token        string
	Tenant       string
	IncusAddress string // sidecar tailnet IP; the remote URL is set to https://<addr>:8443
}

type loginRemoteInstallResult struct {
	RemoteName  string
	IncusConfig string
	Tenant      string
}

type loginRemoteInstaller interface {
	InstallLoginRemote(context.Context, loginRemoteInstallRequest) (loginRemoteInstallResult, error)
}

type loginSetupRequest struct {
	RemoteName        string
	IncusConfig       string
	Tenant            string
	TailscaleAuthKey  string
	TenantPrivateCIDR string
}

type loginSetupResult struct {
	DNS       dnsSetupResult
	Trust     localtrust.Result
	Tailscale tailscale.UpPlan
}

type loginSetupRunner interface {
	RunPostLoginSetup(context.Context, loginSetupRequest) (loginSetupResult, error)
}

type realLoginSetupRunner struct {
	config commandConfig
}

func (r realLoginSetupRunner) RunPostLoginSetup(ctx context.Context, request loginSetupRequest) (loginSetupResult, error) {
	verbose := os.Getenv("VERBOSE") == "1"
	steps := newVerboseStepLogger("login setup", verbose, r.config.stderr)
	incusDir := loginSetupIncusDir(request.IncusConfig)
	incusConfigFile := loginSetupIncusConfigFile(request.IncusConfig)
	restoreEnv := setLoginSetupIncusConfig(incusDir)
	defer restoreEnv()

	config := r.config
	config.adminConfig.Remote = request.RemoteName
	config.adminConfig.Tenant = request.Tenant
	config.adminConfig.Project = ""
	config.tenantStore = incusx.TenantStore{Remote: request.RemoteName, ConfigPath: incusConfigFile}
	config.dnsApplier = incusx.DNSManager{Remote: request.RemoteName, ConfigPath: incusConfigFile}
	config.localDNS = localdns.FileManager{}
	config.localTrust = incusx.LocalTrustManager{Remote: request.RemoteName, ConfigPath: incusConfigFile, Store: localtrust.NewPlatformStore()}
	config.tailscale = incusx.TailscaleManager{Remote: request.RemoteName, ConfigPath: incusConfigFile}

	// v2 tenant: DNS records auto-register in the sidecar CoreDNS and resolve via
	// Tailscale Split DNS; the client is on its own (BYO) tailnet. So the only
	// client-side setup is ensuring the tenant subnet route is accepted + reachable
	// — the v1 local-DNS/trust/tailscale-up steps below don't apply (and their v1
	// tenant lookup can't even see a v2 tenant).
	if strings.TrimSpace(request.TenantPrivateCIDR) != "" {
		if err := steps.run("verify tenant routing", func() error {
			return ensureTenantRouting(ctx, r.config.stdout, request.TenantPrivateCIDR)
		}); err != nil {
			return loginSetupResult{}, err
		}
		return loginSetupResult{}, nil
	}

	var dnsResult dnsSetupResult
	if err := steps.run("setup DNS", func() error {
		var err error
		dnsResult, err = runDNSSetup(ctx, config, request.Tenant)
		return err
	}); err != nil {
		return loginSetupResult{}, err
	}
	var trustPlan localtrust.Plan
	if err := steps.run("plan trust install", func() error {
		var err error
		trustPlan, err = localtrust.PlanInstall(ctx, config.adminConfig, config.tenantStore, localtrust.Request{Reference: request.Tenant})
		return err
	}); err != nil {
		return loginSetupResult{}, err
	}
	if config.localTrust == nil {
		return loginSetupResult{}, fmt.Errorf("local trust executor is not configured")
	}
	if err := writeTrustWarning(config, &rootOptions{output: outputText}, trustPlan); err != nil {
		return loginSetupResult{}, err
	}
	var trustResult localtrust.Result
	if err := steps.run("install trust", func() error {
		var err error
		trustResult, err = config.localTrust.Install(ctx, trustPlan)
		return err
	}); err != nil {
		return loginSetupResult{}, err
	}
	var tailscalePlan tailscale.UpPlan
	if err := steps.run("plan Tailscale up", func() error {
		var err error
		tailscalePlan, err = tailscale.PlanUp(ctx, config.adminConfig, config.tenantStore, tailscale.UpRequest{
			Reference:     request.Tenant,
			AuthKey:       request.TailscaleAuthKey,
			AdvertiseTags: defaultAdvertiseTags(),
		})
		return err
	}); err != nil {
		return loginSetupResult{}, err
	}
	if config.tailscale == nil {
		return loginSetupResult{}, fmt.Errorf("tailscale executor is not configured")
	}
	if err := steps.run("run Tailscale up", func() error {
		return config.tailscale.RunUp(ctx, tailscalePlan, tailscale.RunSession{
			Stdin:  config.stdin,
			Stdout: config.stdout,
			Stderr: config.stderr,
		})
	}); err != nil {
		return loginSetupResult{}, err
	}
	return loginSetupResult{DNS: dnsResult, Trust: trustResult, Tailscale: tailscalePlan}, nil
}

// ensureTenantRouting makes this client accept the tenant's advertised subnet
// route (`tailscale set --accept-routes`) and then verifies the tenant subnet is
// actually reachable — reaching the tenant-bridge gateway's Incus port, which is
// only routable via the sidecar's approved subnet route. Every layer of the path
// (tailscale installed → up → accept-routes → route offered by a peer → probe
// egresses via the tailnet) is checked and reported as it happens, and a failure
// HALTS login with advice specific to the deepest broken layer, because tenant
// machines would otherwise be unreachable.
func ensureTenantRouting(ctx context.Context, stdout io.Writer, cidr string) error {
	cidr = strings.TrimSpace(cidr)
	if cidr == "" {
		return nil
	}
	gateway, err := firstHostInCIDR(cidr)
	if err != nil {
		return nil // can't derive a target; don't block login on a parse error
	}
	fmt.Fprintf(stdout, "Verifying the tenant subnet %s is reachable over the tailnet:\n", cidr)
	check := func(ok bool, format string, args ...any) {
		mark := "✓"
		if !ok {
			mark = "✗"
		}
		fmt.Fprintf(stdout, "  %s %s\n", mark, fmt.Sprintf(format, args...))
	}
	fail := func(advice string) error {
		return fmt.Errorf("tenant subnet %s is not reachable over the tailnet.\n"+
			"  Tenant machines will be unreachable until this is fixed:\n"+
			"%s\n"+
			"  Then re-run `sc login`.", cidr, advice)
	}

	// 1. Is tailscale present at all?
	if _, err := exec.LookPath("tailscale"); err != nil {
		check(false, "tailscale is not installed on this machine")
		return fail("    • Install Tailscale:\n" +
			"          curl -fsSL https://tailscale.com/install.sh | sh\n" +
			"      then join the tailnet the tenant sidecar is on (`tailscale up`).\n" +
			"      The tenant's Incus remote and machines are only reachable over that tailnet.")
	}

	// 2. Accept subnet routes (idempotent, best-effort), then read the client state.
	_ = exec.CommandContext(ctx, "tailscale", "set", "--accept-routes=true").Run()
	state := readTailscaleClientState(ctx)
	switch state.BackendState {
	case "Running":
		self := strings.Join(state.SelfIPs, ", ")
		if self == "" {
			self = "no tailnet IP yet"
		}
		check(true, "tailscale is up (this machine is %s)", self)
	case "":
		check(false, "tailscale state could not be read — is tailscaled running?")
		return fail("    • Start the tailscale daemon (`systemctl start tailscaled`) and join the\n" +
			"      tailnet (`tailscale up`).")
	default:
		check(false, "tailscale is installed but %s", describeTailscaleBackendState(state.BackendState))
		return fail("    • Join the tailnet on this machine: `tailscale up`.")
	}

	// 3. accept-routes actually on?
	routeAll, routeAllKnown := readTailscaleRouteAll(ctx)
	if routeAllKnown {
		check(routeAll, "accept-routes is %s", onOff(routeAll))
	}

	// 4. Does any tailnet peer offer the tenant route, and is one elected primary?
	offered, primary, router := tenantRouteOwner(state, cidr)
	switch {
	case primary:
		online := "online"
		if !router.Online {
			online = "OFFLINE"
		}
		check(true, "route %s is served by peer %q (%s, %s)", cidr, router.HostName, firstNonEmpty(router.IPs), online)
	case offered:
		check(false, "route %s is approved for peer %q but no peer is elected its primary router", cidr, router.HostName)
	default:
		check(false, "no tailnet peer offers the route %s", cidr)
	}

	// 5. The probe is the real gate: a raw TCP connect is not enough, because the
	// gateway address can be coincidentally routable via the client's own LAN,
	// which would falsely pass while tenant machines (other hosts in the /24)
	// stay unreachable. Only a connection whose local endpoint is this machine's
	// Tailscale address (CGNAT 100.64.0.0/10) proves the packet egressed over the
	// tenant's approved subnet route.
	target := net.JoinHostPort(gateway, "8443")
	deadline := time.Now().Add(20 * time.Second)
	var lastDialErr error
	var lastLANAddr string
	for {
		conn, err := net.DialTimeout("tcp", target, 4*time.Second)
		if err == nil {
			viaTailnet := connEgressedViaTailnet(conn)
			local := conn.LocalAddr().String()
			_ = conn.Close()
			if viaTailnet {
				check(true, "probe to %s connected via this machine's tailnet address", target)
				return nil
			}
			lastLANAddr = local
			lastDialErr = nil
		} else {
			lastDialErr = err
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if lastLANAddr != "" {
		check(false, "probe to %s was answered via local address %s, NOT the tailnet", target, lastLANAddr)
	} else {
		check(false, "probe to %s got no answer within 20s (last error: %v)", target, lastDialErr)
	}

	// Advice for the deepest broken layer.
	switch {
	case routeAllKnown && !routeAll:
		return fail("    • This machine does not accept subnet routes and enabling them failed.\n" +
			"      Run `tailscale set --accept-routes` and check `tailscale status` for errors.")
	case !offered:
		return fail("    • Approve the route the tenant sidecar advertises in your Tailscale admin\n" +
			"      console (Machines → the sidecar → Edit route settings → approve " + cidr + "),\n" +
			"      or deploy the auth-app with --tailscale-api-key for automatic approval.\n" +
			"      Also check the sidecar device is online in the admin console.")
	case !primary:
		return fail("    • The route is approved but no peer won the primary-router election —\n" +
			"      usually stale duplicate sidecar devices (same hostname, offline) still\n" +
			"      claiming the route. Delete the old sidecar devices in the Tailscale admin\n" +
			"      console, keeping only the live one.")
	case lastLANAddr != "":
		return fail("    • Traffic to " + cidr + " is short-circuited by an overlapping local\n" +
			"      network (it left via " + lastLANAddr + " instead of the tailnet). A LAN,\n" +
			"      bridge, or another deployment on this machine's network path uses the same\n" +
			"      subnet — remove or renumber the colliding network, or log in from a machine\n" +
			"      whose only path to " + cidr + " is its own tailscale interface.")
	default:
		return fail("    • The route looks healthy but the tenant gateway did not answer. Check the\n" +
			"      sidecar is running and reachable (its Incus API listens on the gateway\n" +
			"      address, port 8443), and that no firewall drops tailnet subnet traffic.")
	}
}

// tailscaleClientState is the subset of `tailscale status --json` the tenant
// routing diagnosis needs.
type tailscaleClientState struct {
	BackendState string
	SelfIPs      []string
	Peers        []tailscalePeerState
}

type tailscalePeerState struct {
	HostName      string
	IPs           []string
	Online        bool
	PrimaryRoutes []string
	AllowedIPs    []string
}

func readTailscaleClientState(ctx context.Context) tailscaleClientState {
	out, err := exec.CommandContext(ctx, "tailscale", "status", "--json").Output()
	if err != nil {
		return tailscaleClientState{}
	}
	return parseTailscaleClientState(out)
}

func parseTailscaleClientState(statusJSON []byte) tailscaleClientState {
	var raw struct {
		BackendState string `json:"BackendState"`
		Self         *struct {
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Self"`
		Peer map[string]struct {
			HostName      string   `json:"HostName"`
			TailscaleIPs  []string `json:"TailscaleIPs"`
			Online        bool     `json:"Online"`
			PrimaryRoutes []string `json:"PrimaryRoutes"`
			AllowedIPs    []string `json:"AllowedIPs"`
		} `json:"Peer"`
	}
	if json.Unmarshal(statusJSON, &raw) != nil {
		return tailscaleClientState{}
	}
	state := tailscaleClientState{BackendState: raw.BackendState}
	if raw.Self != nil {
		state.SelfIPs = raw.Self.TailscaleIPs
	}
	for _, peer := range raw.Peer {
		state.Peers = append(state.Peers, tailscalePeerState{
			HostName:      peer.HostName,
			IPs:           peer.TailscaleIPs,
			Online:        peer.Online,
			PrimaryRoutes: peer.PrimaryRoutes,
			AllowedIPs:    peer.AllowedIPs,
		})
	}
	return state
}

// readTailscaleRouteAll reports whether this client accepts subnet routes
// (`--accept-routes`, pref RouteAll). The second return is false when the pref
// could not be read.
func readTailscaleRouteAll(ctx context.Context) (bool, bool) {
	out, err := exec.CommandContext(ctx, "tailscale", "debug", "prefs").Output()
	if err != nil {
		return false, false
	}
	var prefs struct {
		RouteAll bool `json:"RouteAll"`
	}
	if json.Unmarshal(out, &prefs) != nil {
		return false, false
	}
	return prefs.RouteAll, true
}

// tenantRouteOwner scans the tailnet peers for the tenant subnet route: offered
// means some peer's AllowedIPs carry the route (advertised + approved), primary
// means a peer is elected its subnet router. router is the peer that offers the
// route (the primary one when elected).
func tenantRouteOwner(state tailscaleClientState, cidr string) (offered bool, primary bool, router tailscalePeerState) {
	for _, peer := range state.Peers {
		for _, route := range peer.PrimaryRoutes {
			if route == cidr {
				return true, true, peer
			}
		}
	}
	for _, peer := range state.Peers {
		for _, route := range peer.AllowedIPs {
			if route == cidr {
				offered = true
				router = peer
			}
		}
	}
	return offered, false, router
}

func describeTailscaleBackendState(state string) string {
	switch state {
	case "NeedsLogin":
		return "logged out"
	case "Stopped":
		return "stopped"
	default:
		return "in state " + state
	}
}

func onOff(on bool) string {
	if on {
		return "enabled"
	}
	return "disabled"
}

func firstNonEmpty(values []string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return "no IP"
}

// firstHostInCIDR returns the first usable host in a CIDR (the gateway), e.g.
// 10.250.0.0/24 -> 10.250.0.1.
func firstHostInCIDR(cidr string) (string, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return "", err
	}
	return prefix.Masked().Addr().Next().String(), nil
}

// tailscaleCGNAT is the 100.64.0.0/10 range Tailscale assigns to every node.
var tailscaleCGNAT = netip.MustParsePrefix("100.64.0.0/10")

// connEgressedViaTailnet reports whether an established connection left this
// machine through its Tailscale interface — i.e. its local address is a
// Tailscale node IP. This distinguishes a genuine tenant-subnet route from a
// coincidental LAN path to the same destination address.
func connEgressedViaTailnet(conn net.Conn) bool {
	tcpAddr, ok := conn.LocalAddr().(*net.TCPAddr)
	if !ok {
		return false
	}
	addr, ok := netip.AddrFromSlice(tcpAddr.IP)
	if !ok {
		return false
	}
	return tailscaleCGNAT.Contains(addr.Unmap())
}

func setLoginSetupIncusConfig(path string) func() {
	path = strings.TrimSpace(path)
	if path == "" {
		return func() {}
	}
	old, hadOld := os.LookupEnv("INCUS_CONF")
	_ = os.Setenv("INCUS_CONF", path)
	return func() {
		if hadOld {
			_ = os.Setenv("INCUS_CONF", old)
			return
		}
		_ = os.Unsetenv("INCUS_CONF")
	}
}

func loginSetupIncusDir(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if isIncusConfigFile(path) {
		return filepath.Dir(path)
	}
	return path
}

func loginSetupIncusConfigFile(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if isIncusConfigFile(path) {
		return path
	}
	return filepath.Join(path, "config.yml")
}

func isIncusConfigFile(path string) bool {
	return filepath.Base(path) == "config.yml"
}

func newLoginCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var maxPolls int
	var sshPublicKeyPath string
	var skipSetup bool
	var tailscaleAuthKey string
	var debugApprove bool
	var simulateToken string
	var simulateAs string
	var force bool
	command := &cobra.Command{
		Use:   "login auth-host",
		Short: "Sign in to Sandcastle through the Auth App",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			verbose := os.Getenv("VERBOSE") == "1"
			steps := newVerboseStepLogger("login", verbose, config.stderr)
			verbosef := func(format string, values ...any) {
				if verbose {
					fmt.Fprintf(config.stderr, "[verbose] login: "+format+"\n", values...)
				}
			}
			verbosef("auth host=%s", args[0])
			if !force && tryExistingLogin(cmd.Context(), config, args[0], verbosef) {
				return nil
			}
			// Preflights refuse before the browser dance, not after — every one
			// of these used to fail only once the user had already approved in
			// the browser and waited out provisioning.
			// 1. Enrollment shells out to `incus remote add`, so the incus
			//    client must exist regardless of --skip-setup. (Skipped when a
			//    remote installer is injected — tests don't shell out.)
			if config.loginRemote == nil {
				if _, err := exec.LookPath("incus"); err != nil {
					return fmt.Errorf("the incus client is required: sc login enrolls the tenant remote via\n" +
						"`incus remote add`, and no `incus` binary is on PATH.\n" +
						"    • Debian/Ubuntu: apt-get install incus-client\n" +
						"    • macOS: brew install incus\n" +
						"  Then re-run `sc login`.")
				}
			}
			// 2. A v2 login enrolls a remote at the sidecar's tailnet IP, so a
			//    machine that is not a tailnet node would complete the whole
			//    device flow and then fail the routing setup anyway.
			//    --skip-setup opts out (enroll only).
			if !skipSetup {
				precheck := config.loginTailnetPrecheck
				if precheck == nil {
					precheck = requireTailnetNode
				}
				if err := steps.run("check tailnet membership", func() error {
					return precheck(cmd.Context())
				}); err != nil {
					return err
				}
			}
			var sshKey loginSSHKeyResult
			if err := steps.run("prepare SSH key", func() error {
				var err error
				sshKey, err = prepareLoginSSHKey(loginSSHKeyRequest{
					PublicKeyPath: sshPublicKeyPath,
					ExplicitPath:  cmd.Flags().Changed("ssh-public-key"),
				})
				return err
			}); err != nil {
				return err
			}
			if sshKey.PublicKeyPath != "" {
				verbosef("ssh public key=%s", sshKey.PublicKeyPath)
			}
			fmt.Fprintf(config.stdout, "SSH key: %s\n", sshKey.Fingerprint)
			client := config.authDevice
			if client == nil {
				client = authapp.DeviceClient{BaseURL: args[0]}
			}
			var start authapp.DeviceStartResult
			if err := steps.run("start device login", func() error {
				var err error
				start, err = client.Start(cmd.Context())
				return err
			}); err != nil {
				return err
			}
			fmt.Fprintf(config.stdout, "Open: %s\nCode: %s\n", start.VerificationURI, start.UserCode)
			// The poll that observes the browser approval also provisions the
			// tenant server-side before returning, so the first status change
			// can take a minute or two — say so instead of sitting silent.
			fmt.Fprintln(config.stdout, "(after you approve, provisioning your tenant can take 1-2 minutes on first login)")
			if strings.TrimSpace(simulateToken) != "" {
				asUser := strings.TrimSpace(simulateAs)
				if asUser == "" {
					return fmt.Errorf("--as <github-username> is required with --simulate-token")
				}
				if err := client.SimulateApprove(cmd.Context(), start.UserCode, asUser, simulateToken); err != nil {
					return fmt.Errorf("simulate approve: %w", err)
				}
			} else if debugApprove {
				if err := client.DebugApprove(cmd.Context(), start.UserCode); err != nil {
					return fmt.Errorf("debug approve: %w", err)
				}
			} else if config.openBrowser != nil {
				config.openBrowser(start.VerificationURI)
			}
			if start.Message != "" {
				fmt.Fprintln(config.stdout, start.Message)
			}
			interval := start.Interval
			if interval <= 0 {
				interval = 2
			}
			if maxPolls <= 0 {
				maxPolls = 300
			}
			verbosef("device start: interval=%ds expires_in=%ds", interval, start.ExpiresIn)
			lastMessage := strings.TrimSpace(start.Message)
			awaitingTailnet := false
			approvedAnnounced := false
			tailnetJoinPrinted := false
			for attempt := 0; attempt < maxPolls; attempt++ {
				var result authapp.DevicePollResult
				var pollErr error
				result, pollErr = client.Poll(cmd.Context(), start.DeviceCode, authapp.DevicePollRequest{
					SSHPublicKey:     sshKey.PublicKey,
					LocalUnixUser:    defaultLocalUnixUsername(),
					TailscaleAuthKey: strings.TrimSpace(tailscaleAuthKey),
					AwaitingTailnet:  awaitingTailnet,
				})
				if pollErr != nil {
					return pollErr
				}
				if result.Status != "pending" {
					verbosef("poll result: status=%s expires_in=%ds user=%s remote=%s tenants=%s projects=%s enrollment_present=%t tailscale_auth_key_present=%t message=%s",
						result.Status,
						result.ExpiresIn,
						result.UserKey,
						result.RemoteName,
						strings.Join(result.AccessibleTenants, ","),
						strings.Join(result.Projects, ","),
						result.Token != "",
						result.TailscaleAuthKey != "",
						strings.TrimSpace(result.Message),
					)
				}
				if result.LoginResult != nil {
					verbosef("login result: current_tenant=%s current_project=%s remote=%s ssh_key=%s tailnet_state=%s next=%s",
						result.LoginResult.CurrentTenant,
						result.LoginResult.CurrentProject,
						result.LoginResult.CredentialEnrollment.RemoteName,
						result.LoginResult.SSHKeyFingerprint,
						result.LoginResult.TenantTailnetStatus.State,
						result.LoginResult.NextCommand,
					)
				}
				message := strings.TrimSpace(result.Message)
				if message != "" && message != lastMessage {
					fmt.Fprintln(config.stdout, message)
					lastMessage = message
				}
				switch result.Status {
				case authapp.DeviceStatusPending:
					select {
					case <-cmd.Context().Done():
						return cmd.Context().Err()
					case <-time.After(time.Duration(interval) * time.Second):
					}
				case authapp.DeviceStatusApproved:
					if !approvedAnnounced {
						if result.UserKey != "" {
							fmt.Fprintf(config.stdout, "Approved as %s.\n", result.UserKey)
						} else {
							fmt.Fprintln(config.stdout, "Approved.")
						}
						approvedAnnounced = true
					}
					if err := saveAuthDefaults(args[0], result.CLIAuthToken); err != nil {
						return err
					}
					// BYO tailnet: the tenant supplies the sidecar's tailscale key.
					// Without one the sidecar starts an interactive join — print its
					// login URL and keep polling until it has a tailnet address (the
					// server re-ensures provisioning on each awaiting poll).
					if !skipSetup && result.Token != "" && strings.TrimSpace(result.TenantPrivateCIDR) != "" && strings.TrimSpace(result.IncusRemoteAddress) == "" {
						if !tailnetJoinPrinted {
							if url := strings.TrimSpace(result.TailscaleLoginURL); url != "" {
								fmt.Fprintf(config.stdout, "\nYour tenant sidecar is not on a tailnet yet.\n"+
									"  1. Open  %s\n"+
									"     and log in — that joins the sidecar to YOUR tailnet.\n"+
									"  2. Approve its advertised subnet route (unless a tag autoApprover covers it).\n"+
									"  (tip: `sc login --tailscale-auth-key tskey-...` does this unattended)\n", url)
							}
							fmt.Fprintln(config.stdout, "Waiting for the sidecar to join the tailnet...")
							tailnetJoinPrinted = true
						}
						awaitingTailnet = true
						select {
						case <-cmd.Context().Done():
							return cmd.Context().Err()
						case <-time.After(5 * time.Second):
						}
						continue
					}
					if result.Token != "" {
						tenant := defaultLoginTenant(result.AccessibleTenants)
						remoteName := result.RemoteName
						if remoteName == "" && result.UserKey != "" {
							remoteName = usertrust.RestrictedName(result.UserKey)
						}
						installer := config.loginRemote
						if installer == nil {
							installer = incusLoginRemoteInstaller{stdin: config.stdin, stdout: config.stdout, stderr: config.stderr}
						}
						fmt.Fprintf(config.stdout, "Enrolling Incus remote %q (this generates a client certificate)...\n", remoteName)
						var installed loginRemoteInstallResult
						if err := steps.run("enroll Incus remote", func() error {
							var err error
							installed, err = installer.InstallLoginRemote(cmd.Context(), loginRemoteInstallRequest{
								RemoteName:   remoteName,
								Token:        result.Token,
								Tenant:       tenant,
								IncusAddress: result.IncusRemoteAddress,
							})
							return err
						}); err != nil {
							return err
						}
						fmt.Fprintf(config.stdout, "Remote %q enrolled.\n", installed.RemoteName)
						switch len(result.AccessibleTenants) {
						case 0:
							fmt.Fprintln(config.stdout, "No default tenant set; no accessible tenants were returned.")
						case 1:
							fmt.Fprintf(config.stdout, "Default tenant set to %q.\n", result.AccessibleTenants[0])
						default:
							fmt.Fprintln(config.stdout, "No default tenant set; multiple accessible tenants were returned.")
						}
						if shouldRunLoginSetup(skipSetup, installed.Tenant, result.AccessibleTenants) {
							runner := config.loginSetup
							if runner != nil {
								authKey := strings.TrimSpace(tailscaleAuthKey)
								if authKey == "" {
									authKey = strings.TrimSpace(result.TailscaleAuthKey)
								}
								if authKey == "" {
									authKey = loginTailscaleAuthKeyFromEnv()
								}
								fmt.Fprintf(config.stdout, "Setting up DNS, trust, and Tailscale for %q.\n", installed.Tenant)
								var setup loginSetupResult
								if err := steps.run("post-login setup", func() error {
									var err error
									setup, err = runner.RunPostLoginSetup(cmd.Context(), loginSetupRequest{
										RemoteName:        installed.RemoteName,
										IncusConfig:       installed.IncusConfig,
										Tenant:            installed.Tenant,
										TailscaleAuthKey:  authKey,
										TenantPrivateCIDR: result.TenantPrivateCIDR,
									})
									return err
								}); err != nil {
									return err
								}
								// v2 tenants only run the routing verification (it prints
								// its own check lines); the DNS/trust/tailscale results
								// below belong to the v1 setup and would render as empty
								// fields on a v2 login.
								if strings.TrimSpace(result.TenantPrivateCIDR) == "" {
									fmt.Fprintln(config.stdout, formatDNSSetup(setup.DNS))
									fmt.Fprintln(config.stdout, formatTrustResult(setup.Trust))
									fmt.Fprintln(config.stdout, formatTailscaleUp(setup.Tailscale))
								}
							}
						}
					} else {
						fmt.Fprintln(config.stdout, "No Incus enrollment token returned; remote was not changed.")
					}
					if err := steps.run("verify local tailnet", func() error {
						return verifyLoginTailnet(cmd.Context(), config, result)
					}); err != nil {
						return err
					}
					return nil
				case authapp.DeviceStatusExpired:
					return fmt.Errorf("device login expired")
				case authapp.DeviceStatusDenied:
					return fmt.Errorf("device login denied")
				default:
					return fmt.Errorf("unknown device login status %q", result.Status)
				}
			}
			return fmt.Errorf("device login polling timed out")
		},
	}
	command.Flags().IntVar(&maxPolls, "max-polls", 300, "maximum device login poll attempts")
	command.Flags().BoolVar(&force, "force", false, "re-authenticate even when the saved login for this auth host still works")
	command.Flags().StringVar(&sshPublicKeyPath, "ssh-public-key", "", "SSH public key path to authorize for Machine SSH Access")
	command.Flags().BoolVar(&skipSetup, "skip-setup", false, "skip automatic DNS and Tailscale setup after enrollment")
	command.Flags().StringVar(&tailscaleAuthKey, "tailscale-auth-key", "", "Tailscale auth key for unattended post-login attachment")
	command.Flags().BoolVar(&debugApprove, "debug-approve", false, "auto-approve via /debug/device/approve (requires server --debug-device-user)")
	command.Flags().StringVar(&simulateToken, "simulate-token", "", "DEV ONLY: auto-approve via /oauth/github/simulate using this shared secret (requires server --simulate-github-token); no browser/GitHub")
	command.Flags().StringVar(&simulateAs, "as", "", "GitHub username to log in as when using --simulate-token")
	return command
}

// tryExistingLogin makes `sc login` idempotent: when the saved login for this
// auth host still works — same Auth Hostname, the saved CLI Auth Token is
// accepted by the auth-app, and the enrolled Incus remote answers — it reports
// that and skips the browser device flow. Any doubt falls through to a fresh
// device login (`--force` skips the shortcut entirely).
func tryExistingLogin(ctx context.Context, config commandConfig, authHost string, verbosef func(string, ...any)) bool {
	host := normalizeAuthHostname(authHost)
	saved := normalizeAuthHostname(config.adminConfig.AuthHostname)
	token := strings.TrimSpace(config.adminConfig.AuthToken)
	remote := strings.TrimSpace(config.adminConfig.Remote)
	if host == "" || saved != host || token == "" || remote == "" {
		verbosef("no reusable login for %s (saved host=%q, credential present=%t, remote=%q)", host, saved, token != "", remote)
		return false
	}
	client := config.authTenants
	if client == nil {
		client = authapp.DeviceClient{BaseURL: host, AuthToken: token}
	}
	tenants, err := client.ListTenants(ctx)
	if err != nil {
		verbosef("saved CLI Auth Token was rejected (%v); starting a fresh device login", err)
		return false
	}
	tenant := strings.TrimSpace(config.adminConfig.Tenant)
	if tenant != "" && !tenantAccessListed(tenants, tenant) {
		verbosef("current tenant %q is no longer accessible; starting a fresh device login", tenant)
		return false
	}
	probe := config.loginRemoteProbe
	if probe == nil {
		probe = probeLoginRemote
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := probe(probeCtx, remote); err != nil {
		verbosef("enrolled remote %q did not respond (%v); starting a fresh device login", remote, err)
		return false
	}
	fmt.Fprintf(config.stdout, "Already logged in at %s", host)
	if tenant != "" {
		fmt.Fprintf(config.stdout, " (tenant %q)", tenant)
	}
	fmt.Fprintf(config.stdout, "; remote %q responds.\nRe-run with --force to re-authenticate.\n", remote)
	return true
}

func tenantAccessListed(tenants []authapp.TenantAccessSummary, name string) bool {
	for _, candidate := range tenants {
		if candidate.Tenant == name {
			return true
		}
	}
	return false
}

// requireTailnetNode refuses a full (non --skip-setup) login on a machine that
// is not itself a tailnet node: the v2 remote lives at the sidecar's tailnet IP
// and tenant machines sit behind its subnet route, so login would succeed and
// then the tenant would be unreachable. Being merely resident in a subnet that
// some other router advertises is not enough — subnet-to-subnet traffic does
// not route in a tailnet.
func requireTailnetNode(ctx context.Context) error {
	if _, err := exec.LookPath("tailscale"); err != nil {
		return fmt.Errorf("this machine is not a tailnet node (tailscale is not installed), so the\n" +
			"tenant's Incus remote and machines would be unreachable after login.\n" +
			"    • Install Tailscale:\n" +
			"          curl -fsSL https://tailscale.com/install.sh | sh\n" +
			"      then run `tailscale up` to join the tailnet the tenant sidecar is on\n" +
			"      and re-run `sc login`, or\n" +
			"    • re-run with --skip-setup to enroll anyway (tenant machines stay\n" +
			"      unreachable from this machine).")
	}
	state := readTailscaleClientState(ctx)
	if state.BackendState != "Running" {
		return fmt.Errorf("this machine is not on a tailnet (tailscale is %s), so the tenant's Incus\n"+
			"remote and machines would be unreachable after login.\n"+
			"    • Run `tailscale up` to join the tailnet the tenant sidecar is on, then\n"+
			"      re-run `sc login`, or\n"+
			"    • re-run with --skip-setup to enroll anyway (tenant machines stay\n"+
			"      unreachable from this machine).", describeTailscaleBackendState(state.BackendState))
	}
	return nil
}

// probeLoginRemote verifies the enrolled cert-pinned remote still answers by
// listing its projects over the Incus API.
func probeLoginRemote(ctx context.Context, remote string) error {
	incusDir := resolveIncusDir(remote)
	if incusDir == "" || !remoteExists(incusDir, remote) {
		return fmt.Errorf("remote %q is not enrolled in the local incus config", remote)
	}
	if _, err := exec.LookPath("incus"); err != nil {
		return err
	}
	probe := exec.CommandContext(ctx, "incus", "project", "list", remote+":", "--format", "csv")
	probe.Env = append(os.Environ(), "INCUS_CONF="+incusDir)
	probe.Stdout = io.Discard
	probe.Stderr = io.Discard
	return probe.Run()
}

func verifyLoginTailnet(ctx context.Context, config commandConfig, result authapp.DevicePollResult) error {
	if result.LoginResult == nil || strings.TrimSpace(result.LoginResult.TenantTailnetStatus.Tailnet) == "" {
		return nil
	}
	verifier := config.loginTailnet
	if verifier == nil {
		verifier = localLoginTailnetVerifier{}
	}
	tailnet := result.LoginResult.TenantTailnetStatus.Tailnet
	fmt.Fprintf(config.stdout, "Join Tenant Tailnet %q, then return to this terminal.\n", tailnet)
	status, err := verifier.VerifyTenantTailnet(ctx, tailnet)
	if err != nil {
		return err
	}
	fmt.Fprintf(config.stdout, "Tenant Tailnet %q connected", status.Tailnet)
	if len(status.IPs) > 0 {
		fmt.Fprintf(config.stdout, " with IP %s", status.IPs[0])
	}
	fmt.Fprintln(config.stdout, ".")
	return nil
}

type verboseStepLogger struct {
	prefix  string
	enabled bool
	stderr  io.Writer
}

func newVerboseStepLogger(prefix string, enabled bool, stderr io.Writer) verboseStepLogger {
	return verboseStepLogger{prefix: prefix, enabled: enabled, stderr: stderr}
}

func (l verboseStepLogger) run(label string, fn func() error) error {
	if !l.enabled || l.stderr == nil {
		return fn()
	}
	start := time.Now()
	// The completion line repeats the step label on its own line: nested steps
	// (and the check output of the routing verification) interleave between a
	// step's start and its completion, so a bare " done"/" failed" would be
	// ambiguous — two nested failures used to print two identical " failed"
	// lines with no hint which step each belonged to.
	fmt.Fprintf(l.stderr, "[verbose] %s: %s ...\n", l.prefix, label)
	if err := fn(); err != nil {
		fmt.Fprintf(l.stderr, "[verbose] %s: %s failed (%s)\n", l.prefix, label, formatVerboseStepDuration(time.Since(start)))
		return err
	}
	fmt.Fprintf(l.stderr, "[verbose] %s: %s done (%s)\n", l.prefix, label, formatVerboseStepDuration(time.Since(start)))
	return nil
}

func formatVerboseStepDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return fmt.Sprintf("%dus", duration.Microseconds())
	}
	return duration.Round(time.Millisecond).String()
}

func defaultLoginTenant(tenants []string) string {
	if len(tenants) == 1 {
		return tenants[0]
	}
	return ""
}

func shouldRunLoginSetup(skipSetup bool, tenantName string, accessibleTenants []string) bool {
	return !skipSetup && tenantName != "" && len(accessibleTenants) == 1
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return
	}
	_ = cmd.Start()
}

func loginTailscaleAuthKeyFromEnv() string {
	if authKey := strings.TrimSpace(os.Getenv("SANDCASTLE_TAILSCALE_AUTHKEY")); authKey != "" {
		return authKey
	}
	return strings.TrimSpace(os.Getenv("SANDCASTLE_E2E_TAILSCALE_AUTHKEY"))
}
