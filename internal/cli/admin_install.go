package cli

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/naming"
)

// installV2Prefix maps the install prefix to the v2 Incus project prefix, the
// same rule PlanCreateV2 applies: the default "sc" becomes "sc2", anything else
// is used verbatim.
func installV2Prefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || prefix == naming.DefaultIncusProjectPrefix {
		return naming.V2IncusProjectPrefix
	}
	return prefix
}

// newAdminInstallCommand implements `sc-adm install`: the ONE command that puts
// a complete sandcastle on the local Incus host — the Auth App appliance and
// the broker appliance — with a shared tenant CIDR pool. It refuses to run when
// an installation under the same --prefix already exists.
func newAdminInstallCommand(config commandConfig) *cobra.Command {
	var (
		prefix, cidrPool, baseImage, binaryPath, bridge, storagePool string
		hostname, githubClientID, githubClientSecret, adminUsers     string
		defaultUnixUser, tailscaleAuthKey, tailscaleAPIKey           string
		simulateGitHubToken, tlsMode, brokerPort                     string
	)
	command := &cobra.Command{
		Use:   "install",
		Short: "Install a complete sandcastle (auth-app + broker) on this Incus host",
		Long: "One command for the whole server-side install: deploys the Auth App appliance " +
			"(GitHub login + tenant provisioning) and the broker appliance, sharing one tenant " +
			"CIDR pool. Front the Auth Hostname via sc-edge afterwards. Refuses to run when an " +
			"installation under the same --prefix already exists on this host.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			creator := config.tenantCreator
			v2Prefix := installV2Prefix(prefix)
			authAppInstance := v2Prefix + "-auth-app"
			brokerName := v2Prefix + "-broker"

			// Preflight 1: refuse when this prefix is already installed.
			if existing := detectExistingInstall(cmd.Context(), config, v2Prefix, authAppInstance, brokerName); len(existing) > 0 {
				return fmt.Errorf("a sandcastle installation with prefix %q already exists on this host:\n    %s\n"+
					"  Install side-by-side with a different --prefix, or tear the existing one down first.",
					prefix, strings.Join(existing, "\n    "))
			}
			// Preflight 2: warn when the tenant pool overlaps an address this
			// host already has — the allocator only avoids other TENANTS, so an
			// overlapping local bridge fails later with dnsmasq address-in-use
			// (or silently shadows traffic).
			if warn := cidrPoolOverlapsHost(cidrPool); warn != "" {
				fmt.Fprintf(config.stderr, "WARNING: %s\n", warn)
			}

			if strings.TrimSpace(binaryPath) == "" {
				exe, err := os.Executable()
				if err != nil {
					return fmt.Errorf("resolve binary (pass --binary): %w", err)
				}
				binaryPath = exe
			}
			simulate := strings.TrimSpace(simulateGitHubToken) != ""
			in := bufio.NewReader(config.stdin)
			hostname = promptIfBlank(config.stdout, in, hostname, "Auth Hostname (e.g. sc2.thieso2.dev)")
			if !simulate {
				githubClientID = promptIfBlank(config.stdout, in, githubClientID, "GitHub OAuth client id")
				githubClientSecret = promptIfBlank(config.stdout, in, githubClientSecret, "GitHub OAuth client secret")
			}
			adminUsers = promptIfBlank(config.stdout, in, adminUsers, "Admin GitHub users (comma-separated)")
			if strings.TrimSpace(hostname) == "" || strings.TrimSpace(adminUsers) == "" {
				return fmt.Errorf("auth-hostname and admin-github-users are required")
			}
			if !simulate && (strings.TrimSpace(githubClientID) == "" || strings.TrimSpace(githubClientSecret) == "") {
				return fmt.Errorf("github-client-id and github-client-secret are required (or use --simulate-github-token for dev)")
			}

			fmt.Fprintf(config.stdout, "[1/2] deploying auth-app appliance %s...\n", authAppInstance)
			if err := creator.BootstrapAuthApp(cmd.Context(), incusx.BootstrapAuthAppRequest{
				Project:             "infrastructure",
				Instance:            authAppInstance,
				BaseImage:           baseImage,
				BinaryPath:          binaryPath,
				Bridge:              bridge,
				StoragePool:         storagePool,
				Hostname:            hostname,
				GitHubClientID:      githubClientID,
				GitHubClientSecret:  githubClientSecret,
				AdminGitHubUsers:    splitCommaList(adminUsers),
				DefaultUnixUser:     defaultUnixUser,
				TailscaleAuthKey:    tailscaleAuthKey,
				TailscaleAPIKey:     tailscaleAPIKey,
				SimulateGitHubToken: simulateGitHubToken,
				CIDRPool:            cidrPool,
				ProjectPrefix:       prefix,
				TLSMode:             tlsMode,
			}); err != nil {
				return fmt.Errorf("auth-app deploy: %w", err)
			}
			fmt.Fprintf(config.stdout, "[2/2] deploying broker appliance %s...\n", brokerName)
			if err := creator.BootstrapV2(cmd.Context(), incusx.BootstrapV2Request{
				BaseImage:   baseImage,
				BinaryPath:  binaryPath,
				Bridge:      bridge,
				StoragePool: storagePool,
				Hostname:    hostname,
				CIDRPool:    cidrPool,
				PublicPort:  brokerPort,
				Project:     brokerName,
				Instance:    brokerName,
			}); err != nil {
				return fmt.Errorf("broker bootstrap: %w", err)
			}
			fmt.Fprintf(config.stdout, "sandcastle installed (prefix %q):\n", prefix)
			fmt.Fprintf(config.stdout, "  auth-app: %s (project infrastructure), serving :9444 internally\n", authAppInstance)
			fmt.Fprintf(config.stdout, "  broker:   %s (project %s), :%s\n", brokerName, brokerName, brokerPort)
			fmt.Fprintf(config.stdout, "  tenant CIDR pool: %s (shared; the allocator avoids other tenants' /24s)\n", cidrPool)
			fmt.Fprintf(config.stdout, "Next: front https://%s via sc-edge (reverse_proxy to the auth-app IP :9444),\n", hostname)
			fmt.Fprintln(config.stdout, "then users run: sc login https://"+hostname)
			return nil
		},
	}
	command.Flags().StringVar(&prefix, "prefix", naming.DefaultIncusProjectPrefix, "installation prefix: scopes tenant project names and appliance names; lets several sandcastles share one Incus host")
	command.Flags().StringVar(&cidrPool, "cidr-pool", "10.248.0.0/16", "tenant CIDR pool shared by auth-app and broker (optional; keep pools distinct across installations that share a tailnet)")
	command.Flags().StringVar(&hostname, "auth-hostname", "", "public Auth Hostname; prompted if empty")
	command.Flags().StringVar(&githubClientID, "github-client-id", "", "GitHub OAuth client id; prompted if empty")
	command.Flags().StringVar(&githubClientSecret, "github-client-secret", "", "GitHub OAuth client secret; prompted if empty")
	command.Flags().StringVar(&adminUsers, "admin-github-users", "", "comma-separated admin GitHub usernames; prompted if empty")
	command.Flags().StringVar(&simulateGitHubToken, "simulate-github-token", "", "DEV ONLY: simulated-GitHub mode gated by this shared secret (no real OAuth app)")
	command.Flags().StringVar(&defaultUnixUser, "default-unix-user", "", "default Unix login for provisioned machines")
	command.Flags().StringVar(&tailscaleAuthKey, "tailscale-auth-key", "", "OPTIONAL default Tailscale auth key for tenants that don't bring their own (tenants normally supply theirs at sc login)")
	command.Flags().StringVar(&tailscaleAPIKey, "tailscale-api-key", "", "OPTIONAL Tailscale API key for tenant route auto-approval")
	command.Flags().StringVar(&baseImage, "base-image", incusx.DefaultApplianceImage, "system-container base image for the appliances")
	command.Flags().StringVar(&binaryPath, "binary", "", "path to the fat binary to push (default: this binary)")
	command.Flags().StringVar(&bridge, "bridge", "incusbr0", "bridge the appliance NICs attach to")
	command.Flags().StringVar(&storagePool, "storage-pool", "default", "storage pool for the appliance root disks")
	command.Flags().StringVar(&tlsMode, "infra-tls-mode", "acme", "infrastructure TLS mode")
	command.Flags().StringVar(&brokerPort, "broker-port", "9443", "host port the broker listens on")
	return command
}

// detectExistingInstall lists the components of an installation under the given
// prefix that already exist on this host: the appliances and any tenant
// projects using the prefix.
func detectExistingInstall(ctx context.Context, config commandConfig, v2Prefix string, authAppInstance string, brokerName string) []string {
	found := []string{}
	if config.tenantStore != nil {
		if projects, err := config.tenantStore.ListProjects(ctx); err == nil {
			for _, project := range projects {
				if project.Name == brokerName {
					found = append(found, "broker project "+project.Name)
					continue
				}
				if strings.HasPrefix(project.Name, v2Prefix+"-") {
					found = append(found, "tenant project "+project.Name)
				}
			}
		}
	}
	if instance := detectInstance(config, "infrastructure", authAppInstance); instance != "" {
		found = append(found, instance)
	}
	return found
}

// detectInstance reports "auth-app instance <name>" when the instance exists in
// the project, using the tenant creator's server access.
func detectInstance(config commandConfig, project string, instance string) string {
	if config.tenantCreator.InstanceExists(project, instance) {
		return "auth-app instance " + instance + " (project " + project + ")"
	}
	return ""
}

// cidrPoolOverlapsHost warns when a host interface address falls inside the
// tenant pool.
func cidrPoolOverlapsHost(pool string) string {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(pool))
	if err != nil {
		return ""
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(ipNet.IP.To4())
		if !ok {
			continue
		}
		if prefix.Contains(ip) {
			return fmt.Sprintf("tenant CIDR pool %s overlaps this host's own address %s — tenant /24s from this pool can collide with existing networks; consider a different --cidr-pool", pool, ip)
		}
	}
	return ""
}
