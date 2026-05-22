package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/authapp"
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

func newLoginCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var maxPolls int
	var sshPublicKeyPath string
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
				result, err := client.Poll(cmd.Context(), start.DeviceCode)
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
					} else {
						fmt.Fprintln(config.stdout, "No Incus enrollment token returned; remote was not changed.")
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
	return command
}

func defaultLoginTenant(tenants []string) string {
	if len(tenants) == 1 {
		return tenants[0]
	}
	return ""
}
