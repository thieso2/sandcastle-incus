package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	RemoteName string
	Token      string
	Tenant     string
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
	RemoteName       string
	IncusConfig      string
	Tenant           string
	TailscaleAuthKey string
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
	config.localDNSService = localdns.FileServiceManager{}
	config.localTrust = incusx.LocalTrustManager{Remote: request.RemoteName, ConfigPath: incusConfigFile, Store: localtrust.NewPlatformStore()}
	config.tailscale = incusx.TailscaleManager{Remote: request.RemoteName, ConfigPath: incusConfigFile}

	dnsResult, err := runDNSSetup(ctx, config, request.Tenant)
	if err != nil {
		return loginSetupResult{}, err
	}
	trustPlan, err := localtrust.PlanInstall(ctx, config.adminConfig, config.tenantStore, localtrust.Request{Reference: request.Tenant})
	if err != nil {
		return loginSetupResult{}, err
	}
	if config.localTrust == nil {
		return loginSetupResult{}, fmt.Errorf("local trust executor is not configured")
	}
	if err := writeTrustWarning(config, &rootOptions{output: outputText}, trustPlan); err != nil {
		return loginSetupResult{}, err
	}
	trustResult, err := config.localTrust.Install(ctx, trustPlan)
	if err != nil {
		return loginSetupResult{}, err
	}
	tailscalePlan, err := tailscale.PlanUp(ctx, config.adminConfig, config.tenantStore, tailscale.UpRequest{
		Reference:     request.Tenant,
		AuthKey:       request.TailscaleAuthKey,
		AdvertiseTags: defaultAdvertiseTags(),
	})
	if err != nil {
		return loginSetupResult{}, err
	}
	if config.tailscale == nil {
		return loginSetupResult{}, fmt.Errorf("tailscale executor is not configured")
	}
	if err := config.tailscale.RunUp(ctx, tailscalePlan, tailscale.RunSession{
		Stdin:  config.stdin,
		Stdout: config.stdout,
		Stderr: config.stderr,
	}); err != nil {
		return loginSetupResult{}, err
	}
	return loginSetupResult{DNS: dnsResult, Trust: trustResult, Tailscale: tailscalePlan}, nil
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
	command := &cobra.Command{
		Use:   "login auth-host",
		Short: "Sign in to Sandcastle through the Auth App",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			verbose := os.Getenv("VERBOSE") == "1"
			verbosef := func(format string, values ...any) {
				if verbose {
					fmt.Fprintf(config.stderr, "[verbose] login: "+format+"\n", values...)
				}
			}
			verbosef("auth host=%s", args[0])
			sshKey, err := prepareLoginSSHKey(loginSSHKeyRequest{
				PublicKeyPath: sshPublicKeyPath,
				ExplicitPath:  cmd.Flags().Changed("ssh-public-key"),
			})
			if err != nil {
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
			start, err := client.Start(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(config.stdout, "Open: %s\nCode: %s\n", start.VerificationURI, start.UserCode)
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
				verbosef("poll attempt=%d/%d", attempt+1, maxPolls)
				result, err := client.Poll(cmd.Context(), start.DeviceCode, authapp.DevicePollRequest{
					SSHPublicKey: sshKey.PublicKey,
				})
				if err != nil {
					return err
				}
				verbosef("poll result: status=%s expires_in=%ds user=%s remote=%s tenants=%s message=%s",
					result.Status,
					result.ExpiresIn,
					result.UserKey,
					result.RemoteName,
					strings.Join(result.AccessibleTenants, ","),
					strings.TrimSpace(result.Message),
				)
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
						installed, err := installer.InstallLoginRemote(cmd.Context(), loginRemoteInstallRequest{
							RemoteName: remoteName,
							Token:      result.Token,
							Tenant:     tenant,
						})
						if err != nil {
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
								setup, err := runner.RunPostLoginSetup(cmd.Context(), loginSetupRequest{
									RemoteName:       installed.RemoteName,
									IncusConfig:      installed.IncusConfig,
									Tenant:           installed.Tenant,
									TailscaleAuthKey: authKey,
								})
								if err != nil {
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
					if err := verifyLoginTailnet(cmd.Context(), config, result); err != nil {
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

func defaultLoginTenant(tenants []string) string {
	if len(tenants) == 1 {
		return tenants[0]
	}
	return ""
}

func shouldRunLoginSetup(skipSetup bool, tenantName string, accessibleTenants []string) bool {
	return !skipSetup && tenantName != "" && len(accessibleTenants) == 1
}

func loginTailscaleAuthKeyFromEnv() string {
	if authKey := strings.TrimSpace(os.Getenv("SANDCASTLE_TAILSCALE_AUTHKEY")); authKey != "" {
		return authKey
	}
	return strings.TrimSpace(os.Getenv("SANDCASTLE_E2E_TAILSCALE_AUTHKEY"))
}
