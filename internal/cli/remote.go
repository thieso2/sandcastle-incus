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

<join-token> is the token produced by "sc admin user create" (or
"incus config trust add --generate-certificate" on the server). The token
already contains the server address — no separate address argument is needed.

Incus certs are stored in ~/.config/sandcastle/<name>/incus/ and the remote
is saved as the default in ~/.config/sandcastle/config.yml.

Use --tenant to also set your default tenant name in one step:

  sc remote add sc-acme JOIN_TOKEN --tenant acme`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, joinToken := args[0], args[1]
			incusDir := scconfig.RemoteIncusDir(name)
			if err := os.MkdirAll(incusDir, 0o700); err != nil {
				return fmt.Errorf("create incus config dir: %w", err)
			}

			// Pass the join token as the address argument — incus detects it is
			// a JSON token and extracts the server address from it automatically.
			env := append(os.Environ(), "INCUS_CONF="+incusDir)

			addCmd := exec.CommandContext(cmd.Context(), "incus", "remote", "add", name, joinToken)
			addCmd.Env = env
			addCmd.Stdin = config.stdin
			addCmd.Stdout = config.stdout
			addCmd.Stderr = config.stderr
			if err := addCmd.Run(); err != nil {
				return fmt.Errorf("incus remote add: %w", err)
			}

			switchCmd := exec.CommandContext(cmd.Context(), "incus", "remote", "switch", name)
			switchCmd.Env = env
			switchCmd.Stdout = config.stdout
			switchCmd.Stderr = config.stderr
			if err := switchCmd.Run(); err != nil {
				return fmt.Errorf("incus remote switch: %w", err)
			}
			if err := normalizeRemoteURL(cmd.Context(), name, incusDir, env, config.stderr); err != nil {
				return err
			}

			cfgPath := scconfig.DefaultConfigPath()
			cfg, err := scconfig.LoadSandcastleConfig(cfgPath)
			if err != nil {
				return fmt.Errorf("load sandcastle config: %w", err)
			}
			if cfg.Remote == "" {
				cfg.Remote = name
				fmt.Fprintf(config.stdout, "Default remote set to %q\n", name)
			}
			if tenant != "" {
				cfg.Tenant = tenant
			}
			if err := scconfig.SaveSandcastleConfig(cfgPath, cfg); err != nil {
				return fmt.Errorf("save sandcastle config: %w", err)
			}
			fmt.Fprintf(config.stdout, "Remote %q added. Incus config: %s\n", name, incusDir)
			if cfg.Tenant != "" {
				fmt.Fprintf(config.stdout, "Default tenant set to %q\n", cfg.Tenant)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "Set the default tenant name in ~/.config/sandcastle/config.yml")
	return cmd
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
