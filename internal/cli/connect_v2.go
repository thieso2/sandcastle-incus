package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

// newConnectV2Command implements `sc connect-v2 <tenant>` (ADR-0016): it
// (re)generates the tenant's local Incus config from scratch in an isolated
// config dir — enrolling the tenant's own cert from an enrollment token (first
// time) and adding one cert-pinned remote per project the cert can see. Re-runs
// are idempotent: pass no token to just refresh the per-project remotes after
// new projects were created.
func newConnectV2Command(config commandConfig, opts *rootOptions) *cobra.Command {
	var token, endpoint, configDir, remoteName string
	command := &cobra.Command{
		Use:   "enroll tenant",
		Short: "Enroll a tenant locally from a token (regenerates local incus config + per-project remotes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tenant := strings.TrimSpace(args[0])
			if err := naming.ValidateTenantName(tenant); err != nil {
				return err
			}
			name := strings.TrimSpace(remoteName)
			if name == "" {
				name = usertrust.RemoteInstallName("", tenant)
			}
			dir := strings.TrimSpace(configDir)
			if dir == "" {
				dir = scconfig.SharedIncusDir()
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return fmt.Errorf("create config dir: %w", err)
			}
			if _, err := exec.LookPath("incus"); err != nil {
				return fmt.Errorf("incus CLI not found on PATH")
			}

			// 1. Enroll the base remote from the token (only if not already enrolled).
			if !remoteExists(dir, name) {
				if strings.TrimSpace(token) == "" {
					return fmt.Errorf("tenant %q is not enrolled here; pass --token from `sc-adm tenant create`", tenant)
				}
				if err := runIncus(cmd.Context(), dir, "remote", "add", name, strings.TrimSpace(token)); err != nil {
					return fmt.Errorf("enroll tenant remote: %w", err)
				}
			}

			// 2. Add one cert-pinned remote per project the cert can see.
			projects, err := listRemoteProjects(cmd.Context(), dir, name)
			if err != nil {
				return fmt.Errorf("list projects: %w", err)
			}
			added := 0
			for _, incusProject := range projects {
				short := shortProjectName(incusProject, tenant)
				if short == "" {
					continue
				}
				projectRemote := name + "-" + short
				if remoteExists(dir, projectRemote) {
					continue
				}
				if err := addProjectRemote(cmd.Context(), projectRemote, strings.TrimSpace(endpoint), incusProject, dir); err != nil {
					fmt.Fprintf(config.stderr, "Note: could not add remote %q: %v\n", projectRemote, err)
					continue
				}
				added++
				fmt.Fprintf(config.stdout, "  %s: → %s\n", projectRemote, incusProject)
			}
			// Persist the tenant + remote as the local defaults so every other
			// sc command (list, create, connect, incus, …) resolves this tenant
			// without SANDCASTLE_TENANT — the login path does the same.
			if _, _, err := saveRemoteDefaults(scconfig.DefaultConfigPath(), name, tenant); err != nil {
				fmt.Fprintf(config.stderr, "Note: could not save local defaults: %v\n", err)
			}
			fmt.Fprintf(config.stdout, "connected tenant %q — config at %s (%d project remote(s))\n", tenant, dir, added)
			fmt.Fprintf(config.stdout, "use it with:  INCUS_CONF=%s incus list %s:\n", dir, name)
			return nil
		},
	}
	command.Flags().StringVar(&token, "token", "", "enrollment token from `sc-adm tenant create` (first enroll only)")
	command.Flags().StringVar(&endpoint, "incus-endpoint", "https://big.thieso2.dev:8443", "Incus HTTPS endpoint for per-project remotes")
	command.Flags().StringVar(&configDir, "config-dir", "", "incus config dir to enroll into (default: the shared ~/.config/sandcastle/incus)")
	command.Flags().StringVar(&remoteName, "remote-name", "", "remote name (default sc-<tenant>; prefix installs use sc-<prefix>-<tenant> — copy it from `sc-adm tenant create`)")
	return command
}

func runIncus(ctx context.Context, incusConf string, args ...string) error {
	cmd := exec.CommandContext(ctx, "incus", args...)
	cmd.Env = append(os.Environ(), "INCUS_CONF="+incusConf)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

// listRemoteProjects returns the Incus project names the tenant's cert can see.
func listRemoteProjects(ctx context.Context, incusConf string, remote string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "incus", "project", "list", remote+":", "--format", "csv", "-c", "n")
	cmd.Env = append(os.Environ(), "INCUS_CONF="+incusConf)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var projects []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name := strings.TrimSpace(strings.TrimSuffix(line, " (current)"))
		if name != "" {
			projects = append(projects, name)
		}
	}
	return projects, nil
}

// shortProjectName turns sc2-<tenant>-<project> into <project>.
func shortProjectName(incusProject string, tenant string) string {
	prefix := naming.V2IncusProjectPrefix + "-" + tenant + "-"
	if !strings.HasPrefix(incusProject, prefix) {
		return ""
	}
	return strings.TrimPrefix(incusProject, prefix)
}
