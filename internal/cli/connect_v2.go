package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thieso2/sandcastle-incus/internal/naming"
)

// newConnectV2Command implements `sc connect-v2 <tenant>` (ADR-0016): it
// (re)generates the tenant's local Incus config from scratch in an isolated
// config dir — enrolling the tenant's own cert from an enrollment token (first
// time) and adding one cert-pinned remote per project the cert can see. Re-runs
// are idempotent: pass no token to just refresh the per-project remotes after
// new projects were created.
func newConnectV2Command(config commandConfig, opts *rootOptions) *cobra.Command {
	var token, endpoint, configDir string
	command := &cobra.Command{
		Use:   "connect-v2 tenant",
		Short: "Regenerate a tenant's local incus config (enroll + per-project remotes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tenant := strings.TrimSpace(args[0])
			if err := naming.ValidateTenantName(tenant); err != nil {
				return err
			}
			dir := strings.TrimSpace(configDir)
			if dir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				dir = filepath.Join(home, ".config", "sandcastle", tenant, "incus")
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return fmt.Errorf("create config dir: %w", err)
			}
			if _, err := exec.LookPath("incus"); err != nil {
				return fmt.Errorf("incus CLI not found on PATH")
			}

			// 1. Enroll the base remote from the token (only if not already enrolled).
			if !remoteExists(dir, tenant) {
				if strings.TrimSpace(token) == "" {
					return fmt.Errorf("tenant %q is not enrolled here; pass --token from `sc-adm tenant create-v2`", tenant)
				}
				if err := runIncus(cmd.Context(), dir, "remote", "add", tenant, strings.TrimSpace(token)); err != nil {
					return fmt.Errorf("enroll tenant remote: %w", err)
				}
			}

			// 2. Add one cert-pinned remote per project the cert can see.
			projects, err := listRemoteProjects(cmd.Context(), dir, tenant)
			if err != nil {
				return fmt.Errorf("list projects: %w", err)
			}
			added := 0
			for _, incusProject := range projects {
				short := shortProjectName(incusProject, tenant)
				if short == "" {
					continue
				}
				name := tenant + "-" + short
				if remoteExists(dir, name) {
					continue
				}
				if err := addProjectRemote(cmd.Context(), name, strings.TrimSpace(endpoint), incusProject, dir); err != nil {
					fmt.Fprintf(config.stderr, "Note: could not add remote %q: %v\n", name, err)
					continue
				}
				added++
				fmt.Fprintf(config.stdout, "  %s: → %s\n", name, incusProject)
			}
			fmt.Fprintf(config.stdout, "connected tenant %q — config at %s (%d project remote(s))\n", tenant, dir, added)
			fmt.Fprintf(config.stdout, "use it with:  INCUS_CONF=%s incus list %s:\n", dir, tenant)
			return nil
		},
	}
	command.Flags().StringVar(&token, "token", "", "enrollment token from `sc-adm tenant create-v2` (first connect only)")
	command.Flags().StringVar(&endpoint, "incus-endpoint", "https://big.thieso2.dev:8443", "Incus HTTPS endpoint for per-project remotes")
	command.Flags().StringVar(&configDir, "config-dir", "", "incus config dir to regenerate (default: ~/.config/sandcastle/<tenant>/incus)")
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
