package cli

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"gopkg.in/yaml.v2"
)

func newRemoteCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Manage Sandcastle remotes",
	}
	cmd.AddCommand(newRemoteAddCommand(config, opts))
	return cmd
}

func newRemoteAddCommand(config commandConfig, _ *rootOptions) *cobra.Command {
	var tenant string
	cmd := &cobra.Command{
		Use:   "add <name> <join-token>",
		Short: "Add a Sandcastle remote using an Incus join token",
		Long: `Add a Sandcastle remote.

<join-token> is the token produced by "sandcastle-admin user create" (or
"incus config trust add --generate-certificate" on the server). The token
already contains the server address — no separate address argument is needed.

Incus certs are stored in ~/.config/sandcastle/<name>/incus/ and the remote
is saved as the default in ~/.config/sandcastle/config.yml.

Use --tenant to also set your default tenant name in one step:

  sc remote add sc-acme JOIN_TOKEN --tenant acme`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := addIncusRemoteWithToken(cmd.Context(), remoteAddIO{
				stdin:  config.stdin,
				stdout: config.stdout,
				stderr: config.stderr,
			}, args[0], args[1], tenant, "")
			if err != nil {
				return err
			}
			if result.DefaultRemoteSet {
				fmt.Fprintf(config.stdout, "Default remote set to %q\n", result.RemoteName)
			}
			fmt.Fprintf(config.stdout, "Remote %q added. Incus config: %s\n", result.RemoteName, result.IncusConfig)
			if result.Tenant != "" {
				fmt.Fprintf(config.stdout, "Default tenant set to %q\n", result.Tenant)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "Set the default tenant name in ~/.config/sandcastle/config.yml")
	return cmd
}

type remoteAddIO struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

type remoteAddResult struct {
	RemoteName       string
	IncusConfig      string
	Tenant           string
	DefaultRemoteSet bool
}

type incusLoginRemoteInstaller struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func (i incusLoginRemoteInstaller) InstallLoginRemote(ctx context.Context, request loginRemoteInstallRequest) (loginRemoteInstallResult, error) {
	result, err := addIncusRemoteWithToken(ctx, remoteAddIO{stdin: i.stdin, stdout: i.stdout, stderr: i.stderr}, request.RemoteName, request.Token, request.Tenant, request.IncusAddress)
	if err != nil {
		return loginRemoteInstallResult{}, err
	}
	return loginRemoteInstallResult{RemoteName: result.RemoteName, IncusConfig: result.IncusConfig, Tenant: result.Tenant}, nil
}

// addIncusRemoteWithToken redeems the join token into a per-remote Incus config.
// When incusAddress is set (the sidecar's tailnet IP — ADR-0017), the remote URL
// is pinned to https://<addr>:8443 so the connection rides the tenant tailnet via
// the sidecar proxy; otherwise the address the token advertised is normalized.
func addIncusRemoteWithToken(ctx context.Context, ioConfig remoteAddIO, name string, joinToken string, tenant string, incusAddress string) (remoteAddResult, error) {
	// ONE shared incus config dir for every enrollment: the client keypair in
	// it is the shared identity across installs, and each install is just a
	// remote (`incus remote switch sc-id-<tenant>`). Never wipe the dir —
	// other installs' remotes and the shared keypair live here; replace only
	// THIS remote.
	incusDir := scconfig.SharedIncusDir()
	if err := os.MkdirAll(incusDir, 0o700); err != nil {
		return remoteAddResult{}, fmt.Errorf("create incus config dir: %w", err)
	}

	env := append(os.Environ(), "INCUS_CONF="+incusDir)
	if remoteExists(incusDir, name) {
		switchAway := exec.CommandContext(ctx, "incus", "remote", "switch", "local")
		switchAway.Env = env
		_ = switchAway.Run()
		removeCmd := exec.CommandContext(ctx, "incus", "remote", "remove", name)
		removeCmd.Env = env
		removeCmd.Stderr = ioConfig.stderr
		if err := removeCmd.Run(); err != nil {
			return remoteAddResult{}, fmt.Errorf("replace existing remote %s: %w", name, err)
		}
	}
	addCmd := exec.CommandContext(ctx, "incus", "remote", "add", name, joinToken)
	addCmd.Env = env
	addCmd.Stdin = ioConfig.stdin
	addCmd.Stdout = ioConfig.stdout
	addCmd.Stderr = ioConfig.stderr
	if err := addCmd.Run(); err != nil {
		return remoteAddResult{}, fmt.Errorf("incus remote add: %w", err)
	}

	switchCmd := exec.CommandContext(ctx, "incus", "remote", "switch", name)
	switchCmd.Env = env
	switchCmd.Stdout = ioConfig.stdout
	switchCmd.Stderr = ioConfig.stderr
	if err := switchCmd.Run(); err != nil {
		return remoteAddResult{}, fmt.Errorf("incus remote switch: %w", err)
	}
	if strings.TrimSpace(incusAddress) != "" {
		// Pin the remote at the sidecar's tailnet endpoint; the sidecar proxies it
		// to the host's Incus. Skip the token-address normalization entirely.
		url := "https://" + net.JoinHostPort(strings.TrimSpace(incusAddress), "8443")
		setURLCmd := exec.CommandContext(ctx, "incus", "remote", "set-url", name, url)
		setURLCmd.Env = env
		setURLCmd.Stderr = ioConfig.stderr
		if err := setURLCmd.Run(); err != nil {
			return remoteAddResult{}, fmt.Errorf("incus remote set-url %s: %w", url, err)
		}
	} else if err := normalizeRemoteURL(ctx, name, incusDir, env, ioConfig.stderr); err != nil {
		return remoteAddResult{}, err
	}

	cfgPath := scconfig.DefaultConfigPath()
	defaultRemoteSet, tenant, err := saveRemoteDefaults(cfgPath, name, tenant)
	if err != nil {
		return remoteAddResult{}, err
	}
	return remoteAddResult{RemoteName: name, IncusConfig: incusDir, Tenant: tenant, DefaultRemoteSet: defaultRemoteSet}, nil
}

func remoteExists(incusDir string, name string) bool {
	data, err := os.ReadFile(filepath.Join(incusDir, "config.yml"))
	if err != nil {
		return false
	}
	var config struct {
		Remotes map[string]any `yaml:"remotes"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return false
	}
	_, ok := config.Remotes[name]
	return ok
}

func saveRemoteDefaults(cfgPath string, name string, tenant string) (bool, string, error) {
	cfg, err := scconfig.LoadSandcastleConfig(cfgPath)
	if err != nil {
		return false, "", fmt.Errorf("load sandcastle config: %w", err)
	}
	defaultRemoteSet := false
	if cfg.Remote != name {
		cfg.Remote = name
		defaultRemoteSet = true
	}
	if tenant != "" {
		cfg.Tenant = tenant
	}
	if err := scconfig.SaveSandcastleConfig(cfgPath, cfg); err != nil {
		return false, "", fmt.Errorf("save sandcastle config: %w", err)
	}
	return defaultRemoteSet, cfg.Tenant, nil
}

func normalizeRemoteURL(ctx context.Context, name string, incusDir string, env []string, stderr io.Writer) error {
	configPath := filepath.Join(incusDir, "config.yml")
	certPath := filepath.Join(incusDir, "servercerts", name+".crt")
	normalized, ok, err := normalizedRemoteURL(configPath, name, certPath)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	setURLCmd := exec.CommandContext(ctx, "incus", "remote", "set-url", name, normalized)
	setURLCmd.Env = env
	setURLCmd.Stderr = stderr
	if err := setURLCmd.Run(); err != nil {
		return fmt.Errorf("incus remote set-url: %w", err)
	}
	return nil
}

func normalizedRemoteURL(configPath string, name string, certPath string) (string, bool, error) {
	addr, err := remoteAddress(configPath, name)
	if err != nil {
		return "", false, err
	}
	parsed, err := url.Parse(addr)
	if err != nil {
		return "", false, fmt.Errorf("parse remote address %q: %w", addr, err)
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return "", false, nil
	}
	if net.ParseIP(host) == nil {
		return "", false, nil
	}
	dnsName, err := firstCertificateDNSName(certPath)
	if err != nil {
		return "", false, err
	}
	if dnsName == "" {
		return "", false, nil
	}
	parsed.Host = net.JoinHostPort(dnsName, port)
	return parsed.String(), true, nil
}

func remoteAddress(configPath string, name string) (string, error) {
	content, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("read incus config: %w", err)
	}
	var cfg struct {
		Remotes map[string]struct {
			Addr string `yaml:"addr"`
		} `yaml:"remotes"`
	}
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return "", fmt.Errorf("parse incus config: %w", err)
	}
	remote, ok := cfg.Remotes[name]
	if !ok {
		return "", fmt.Errorf("remote %q not found in incus config", name)
	}
	return remote.Addr, nil
}

func firstCertificateDNSName(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read remote server certificate: %w", err)
	}
	block, _ := pem.Decode(content)
	if block == nil {
		return "", fmt.Errorf("parse remote server certificate: missing PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse remote server certificate: %w", err)
	}
	for _, name := range cert.DNSNames {
		name = strings.TrimSpace(name)
		if name != "" {
			return name, nil
		}
	}
	return "", nil
}
