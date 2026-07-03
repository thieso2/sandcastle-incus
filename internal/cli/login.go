package cli

import (
	"context"
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
	if err := steps.run("verify tenant routing", func() error {
		return ensureTenantRouting(ctx, r.config.stdout, request.TenantPrivateCIDR)
	}); err != nil {
		return loginSetupResult{}, err
	}
	return loginSetupResult{DNS: dnsResult, Trust: trustResult, Tailscale: tailscalePlan}, nil
}

// ensureTenantRouting makes this client accept the tenant's advertised subnet
// route (`tailscale set --accept-routes`) and then verifies the tenant subnet is
// actually reachable — reaching the tenant-bridge gateway's Incus port, which is
// only routable via the sidecar's approved subnet route. If it is not reachable
// it HALTS with guidance, because tenant machines would otherwise be unreachable.
func ensureTenantRouting(ctx context.Context, stdout io.Writer, cidr string) error {
	cidr = strings.TrimSpace(cidr)
	if cidr == "" {
		return nil
	}
	gateway, err := firstHostInCIDR(cidr)
	if err != nil {
		return nil // can't derive a target; don't block login on a parse error
	}
	// Best-effort: accept subnet routes on this machine's own tailnet. Ignore
	// errors — the client may not run tailscale, or may already accept routes;
	// the reachability check below is the real gate.
	if _, err := exec.LookPath("tailscale"); err == nil {
		_ = exec.CommandContext(ctx, "tailscale", "set", "--accept-routes=true").Run()
	}
	target := net.JoinHostPort(gateway, "8443")
	deadline := time.Now().Add(20 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", target, 4*time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	fmt.Fprintln(stdout)
	return fmt.Errorf("tenant subnet %s is not routable from this machine.\n"+
		"  Tenant machines will be unreachable until the subnet route is set up:\n"+
		"    • Approve the route the tenant sidecar advertises in your Tailscale admin\n"+
		"      console (Machines → the sidecar → Edit route settings → approve %s), or\n"+
		"    • deploy the auth-app with --tailscale-api-key for automatic approval.\n"+
		"  Also ensure this machine accepts routes (`tailscale set --accept-routes`).\n"+
		"  Then re-run `sc login`.", cidr, cidr)
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
			for attempt := 0; attempt < maxPolls; attempt++ {
				var result authapp.DevicePollResult
				var pollErr error
				result, pollErr = client.Poll(cmd.Context(), start.DeviceCode, authapp.DevicePollRequest{
					SSHPublicKey:  sshKey.PublicKey,
					LocalUnixUser: defaultLocalUnixUsername(),
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
					if result.UserKey != "" {
						fmt.Fprintf(config.stdout, "Approved as %s.\n", result.UserKey)
					} else {
						fmt.Fprintln(config.stdout, "Approved.")
					}
					if err := saveAuthDefaults(args[0], result.CLIAuthToken); err != nil {
						return err
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
								fmt.Fprintln(config.stdout, formatDNSSetup(setup.DNS))
								fmt.Fprintln(config.stdout, formatTrustResult(setup.Trust))
								fmt.Fprintln(config.stdout, formatTailscaleUp(setup.Tailscale))
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
	command.Flags().StringVar(&sshPublicKeyPath, "ssh-public-key", "", "SSH public key path to authorize for Machine SSH Access")
	command.Flags().BoolVar(&skipSetup, "skip-setup", false, "skip automatic DNS and Tailscale setup after enrollment")
	command.Flags().StringVar(&tailscaleAuthKey, "tailscale-auth-key", "", "Tailscale auth key for unattended post-login attachment")
	command.Flags().BoolVar(&debugApprove, "debug-approve", false, "auto-approve via /debug/device/approve (requires server --debug-device-user)")
	command.Flags().StringVar(&simulateToken, "simulate-token", "", "DEV ONLY: auto-approve via /oauth/github/simulate using this shared secret (requires server --simulate-github-token); no browser/GitHub")
	command.Flags().StringVar(&simulateAs, "as", "", "GitHub username to log in as when using --simulate-token")
	return command
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
	fmt.Fprintf(l.stderr, "[verbose] %s: %s ...", l.prefix, label)
	if err := fn(); err != nil {
		fmt.Fprintf(l.stderr, " failed (%s)\n", formatVerboseStepDuration(time.Since(start)))
		return err
	}
	fmt.Fprintf(l.stderr, " done (%s)\n", formatVerboseStepDuration(time.Since(start)))
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
