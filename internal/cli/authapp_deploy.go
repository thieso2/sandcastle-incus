package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thieso2/sandcastle-incus/internal/incusx"
)

// newAdminAuthAppDeployCommand implements `sc admin auth-app deploy`: it stands
// up the Auth App as a system-container appliance on the Incus host — creating
// the container, copying THIS binary into it, and running `auth-app serve` under
// systemd. Required values not supplied via flags are prompted for on stdin, so
// it works both interactively and scripted (flags/pipe).
func newAdminAuthAppDeployCommand(config commandConfig) *cobra.Command {
	var (
		project, instance, baseImage, binaryPath, bridge, storagePool string
		hostname, githubClientID, githubClientSecret, adminUsers      string
		defaultUnixUser, tailscaleAuthKey, debugDeviceUser            string
		cidrPool, projectPrefix, infraProject, tlsMode                string
		tenantBaseImage, tenantAIImage                                string
	)
	command := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy the Auth App as an appliance on the Incus host (interactive)",
		Long: "Run on (or against) the Incus host. Creates a system container, copies this binary " +
			"into it, and runs `auth-app serve` under systemd with the host admin socket mounted. The " +
			"appliance has no host port — front it at its public hostname via the sc-edge reverse proxy. " +
			"Required values not passed as flags are prompted for on the command line.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			creator := config.tenantCreator
			if strings.TrimSpace(binaryPath) == "" {
				exe, err := os.Executable()
				if err != nil {
					return fmt.Errorf("resolve binary (pass --binary): %w", err)
				}
				binaryPath = exe
			}

			in := bufio.NewReader(config.stdin)
			hostname = promptIfBlank(config.stdout, in, hostname, "Auth Hostname (e.g. sc2.thieso2.dev)")
			githubClientID = promptIfBlank(config.stdout, in, githubClientID, "GitHub OAuth client id")
			githubClientSecret = promptIfBlank(config.stdout, in, githubClientSecret, "GitHub OAuth client secret")
			adminUsers = promptIfBlank(config.stdout, in, adminUsers, "Admin GitHub users (comma-separated)")

			for label, v := range map[string]string{
				"auth-hostname":        hostname,
				"github-client-id":     githubClientID,
				"github-client-secret": githubClientSecret,
				"admin-github-users":   adminUsers,
			} {
				if strings.TrimSpace(v) == "" {
					return fmt.Errorf("%s is required", label)
				}
			}

			if err := creator.BootstrapAuthApp(cmd.Context(), incusx.BootstrapAuthAppRequest{
				Project:            project,
				Instance:           instance,
				BaseImage:          baseImage,
				BinaryPath:         binaryPath,
				Bridge:             bridge,
				StoragePool:        storagePool,
				Hostname:           hostname,
				GitHubClientID:     githubClientID,
				GitHubClientSecret: githubClientSecret,
				AdminGitHubUsers:   splitCommaList(adminUsers),
				DefaultUnixUser:    defaultUnixUser,
				TailscaleAuthKey:   tailscaleAuthKey,
				DebugDeviceUser:    debugDeviceUser,
				CIDRPool:           cidrPool,
				ProjectPrefix:      projectPrefix,
				InfraProject:       infraProject,
				TLSMode:            tlsMode,
				BaseImageRef:       tenantBaseImage,
				AIImageRef:         tenantAIImage,
			}); err != nil {
				return err
			}
			fmt.Fprintf(config.stdout,
				"auth-app deployed: %s (project %s), serving %s internally.\n"+
					"Next: add an sc-edge vhost terminating %s and reverse-proxying to http://<%s bridge ip>%s\n",
				instance, project, incusx.AuthAppListen, hostname, instance, incusx.AuthAppListen)
			return nil
		},
	}
	command.Flags().StringVar(&project, "project", incusx.AuthAppDefaultProject, "Incus project for the appliance")
	command.Flags().StringVar(&instance, "instance", incusx.AuthAppDefaultInstance, "appliance instance name")
	command.Flags().StringVar(&baseImage, "base-image", incusx.DefaultApplianceImage, "system-container base image (a stock systemd image; the fat binary is copied in)")
	command.Flags().StringVar(&binaryPath, "binary", "", "path to the fat binary to push (default: this binary)")
	command.Flags().StringVar(&bridge, "bridge", "incusbr0", "bridge the appliance NIC attaches to")
	command.Flags().StringVar(&storagePool, "storage-pool", "default", "storage pool for the appliance root disk")
	command.Flags().StringVar(&hostname, "auth-hostname", "", "public Auth Hostname; prompted if empty")
	command.Flags().StringVar(&githubClientID, "github-client-id", "", "GitHub OAuth client id; prompted if empty")
	command.Flags().StringVar(&githubClientSecret, "github-client-secret", "", "GitHub OAuth client secret; prompted if empty")
	command.Flags().StringVar(&adminUsers, "admin-github-users", "", "comma-separated admin GitHub usernames; prompted if empty")
	command.Flags().StringVar(&defaultUnixUser, "default-unix-user", "", "default Unix login for provisioned machines")
	command.Flags().StringVar(&tailscaleAuthKey, "tailscale-auth-key", "", "Tailscale auth key handed to approved device logins")
	command.Flags().StringVar(&debugDeviceUser, "debug-device-user", "", "enable debug device approval as this allowlisted user")
	command.Flags().StringVar(&cidrPool, "cidr-pool", "10.248.0.0/16", "tenant CIDR pool the Auth App allocates from")
	command.Flags().StringVar(&projectPrefix, "project-prefix", "sc", "Incus project name prefix for provisioned tenants")
	command.Flags().StringVar(&infraProject, "infra-project", "sc-infra", "infrastructure project used for provisioning")
	command.Flags().StringVar(&tlsMode, "infra-tls-mode", "acme", "infrastructure TLS mode")
	command.Flags().StringVar(&tenantBaseImage, "tenant-base-image", "sandcastle/base:latest", "base image tenants are built from")
	command.Flags().StringVar(&tenantAIImage, "tenant-ai-image", "sandcastle/ai:latest", "AI image tenants can use")
	return command
}

// promptIfBlank returns value when non-empty, otherwise prints "label: " and
// reads one line from in.
func promptIfBlank(stdout io.Writer, in *bufio.Reader, value, label string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	fmt.Fprintf(stdout, "%s: ", label)
	line, _ := in.ReadString('\n')
	return strings.TrimSpace(line)
}

func splitCommaList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
