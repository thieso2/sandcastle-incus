package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/authapp"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/localdns"
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
	Tenant           string
	TailscaleAuthKey string
}

type loginSetupResult struct {
	DNS       dnsSetupResult
	Tailscale tailscale.UpPlan
}

type loginSetupRunner interface {
	RunPostLoginSetup(context.Context, loginSetupRequest) (loginSetupResult, error)
}

type realLoginSetupRunner struct {
	config commandConfig
}

func (r realLoginSetupRunner) RunPostLoginSetup(ctx context.Context, request loginSetupRequest) (loginSetupResult, error) {
	config := r.config
	config.adminConfig.Remote = request.RemoteName
	config.adminConfig.Tenant = request.Tenant
	config.adminConfig.Project = ""
	config.tenantStore = incusx.NewTenantStore(request.RemoteName)
	config.dnsApplier = incusx.NewDNSManager(request.RemoteName)
	config.localDNS = localdns.FileManager{}
	config.localDNSService = localdns.FileServiceManager{}
	config.tailscale = incusx.NewTailscaleManager(request.RemoteName)

	dnsResult, err := runDNSSetup(ctx, config, request.Tenant)
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
	return loginSetupResult{DNS: dnsResult, Tailscale: tailscalePlan}, nil
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
			sshKey, err := prepareLoginSSHKey(loginSSHKeyRequest{
				PublicKeyPath: sshPublicKeyPath,
				ExplicitPath:  cmd.Flags().Changed("ssh-public-key"),
			})
			if err != nil {
				return err
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
			for attempt := 0; attempt < maxPolls; attempt++ {
				result, err := client.Poll(cmd.Context(), start.DeviceCode, authapp.DevicePollRequest{
					SSHPublicKey: sshKey.PublicKey,
				})
				if err != nil {
					return err
				}
				if result.Message != "" {
					fmt.Fprintln(config.stdout, result.Message)
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
									authKey = loginTailscaleAuthKeyFromEnv()
								}
								fmt.Fprintf(config.stdout, "Setting up DNS and Tailscale for %q.\n", installed.Tenant)
								setup, err := runner.RunPostLoginSetup(cmd.Context(), loginSetupRequest{
									RemoteName:       installed.RemoteName,
									Tenant:           installed.Tenant,
									TailscaleAuthKey: authKey,
								})
								if err != nil {
									return err
								}
								fmt.Fprintln(config.stdout, formatDNSSetup(setup.DNS))
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
	return strings.TrimSpace(os.Getenv("SANDCASTLE_TAILSCALE_AUTHKEY"))
}
