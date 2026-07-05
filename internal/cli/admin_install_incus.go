package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

// newAdminInstallIncusCommand implements `sc-adm install-incus`: puts the
// latest Incus (Zabbly stable) on a Debian-based host and initializes it, so a
// fresh box goes `sc-adm install-incus` → `sc-adm install --auth-hostname …`.
// Debian's own archive ships the old LTS; sandcastle's v2 features (shared
// volumes across CT+VM, security.shifted) want the current release.
func newAdminInstallIncusCommand(config commandConfig) *cobra.Command {
	var codename string
	var skipInit bool
	command := &cobra.Command{
		Use:   "install-incus",
		Short: "Install the latest Incus (Zabbly stable) on this Debian-based host",
		Long: "Sets up the Zabbly stable apt repository, installs (or upgrades to) the current Incus " +
			"release, runs `incus admin init --minimal` when the daemon is uninitialized, and adds the " +
			"invoking user to incus-admin. Idempotent — safe to re-run. Requires root (run with sudo).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Geteuid() != 0 {
				return fmt.Errorf("install-incus needs root: re-run with sudo")
			}
			osRelease, err := os.ReadFile("/etc/os-release")
			if err != nil {
				return fmt.Errorf("read /etc/os-release: %w (Debian-based hosts only)", err)
			}
			detected, debianLike := parseOSRelease(string(osRelease))
			if !debianLike {
				return fmt.Errorf("this host does not look Debian-based; install-incus supports Debian/Ubuntu (Zabbly repo)")
			}
			suite := strings.TrimSpace(codename)
			if suite == "" {
				suite = detected
			}
			if suite == "" {
				return fmt.Errorf("could not detect the distribution codename; pass --codename (e.g. trixie, bookworm, noble)")
			}
			adminUser := strings.TrimSpace(os.Getenv("SUDO_USER"))
			script := installIncusScript(suite, adminUser, skipInit)
			fmt.Fprintf(config.stdout, "installing latest Incus (Zabbly stable, suite %s)...\n", suite)
			shell := exec.CommandContext(cmd.Context(), "bash", "-c", script)
			shell.Stdout = config.stdout
			shell.Stderr = config.stderr
			if err := shell.Run(); err != nil {
				return fmt.Errorf("incus install: %w", err)
			}
			if adminUser != "" {
				fmt.Fprintf(config.stdout, "NOTE: %s was added to incus-admin — log out and back in for it to take effect.\n", adminUser)
			}
			fmt.Fprintln(config.stdout, "Next: sc-adm install --auth-hostname <host> …")
			return nil
		},
	}
	command.Flags().StringVar(&codename, "codename", "", "apt suite for the Zabbly repo (default: detected from /etc/os-release, e.g. trixie)")
	command.Flags().BoolVar(&skipInit, "skip-init", false, "do not run `incus admin init --minimal` after installing")
	return command
}

// parseOSRelease extracts the codename and whether the distro is Debian-based.
func parseOSRelease(content string) (codename string, debianLike bool) {
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"`)
		switch key {
		case "VERSION_CODENAME":
			codename = value
		case "ID":
			if value == "debian" || value == "ubuntu" {
				debianLike = true
			}
		case "ID_LIKE":
			if strings.Contains(value, "debian") || strings.Contains(value, "ubuntu") {
				debianLike = true
			}
		}
	}
	return codename, debianLike
}

// installIncusScript renders the idempotent install script: Zabbly key + repo
// (deb822), apt install, minimal init when uninitialized, incus-admin group.
func installIncusScript(suite string, adminUser string, skipInit bool) string {
	lines := []string{
		"set -euo pipefail",
		"export DEBIAN_FRONTEND=noninteractive",
		"mkdir -p /etc/apt/keyrings",
		"curl -fsSL https://pkgs.zabbly.com/key.asc -o /etc/apt/keyrings/zabbly.asc",
		"cat > /etc/apt/sources.list.d/zabbly-incus-stable.sources <<SRC",
		"Enabled: yes",
		"Types: deb",
		"URIs: https://pkgs.zabbly.com/incus/stable",
		"Suites: " + suite,
		"Components: main",
		"Architectures: $(dpkg --print-architecture)",
		"Signed-By: /etc/apt/keyrings/zabbly.asc",
		"SRC",
		"apt-get update -qq",
		"apt-get install -y -qq incus",
	}
	if !skipInit {
		lines = append(lines,
			// Initialized daemons have a root disk on the default profile;
			// a fresh `incus admin init --minimal` run is only for the rest.
			`if incus profile device get default root pool >/dev/null 2>&1; then`,
			`  echo "incus already initialized — skipping init"`,
			`else`,
			`  incus admin init --minimal`,
			`fi`,
		)
	}
	if strings.TrimSpace(adminUser) != "" {
		lines = append(lines, "usermod -aG incus-admin "+strings.TrimSpace(adminUser))
	}
	lines = append(lines, `echo "incus $(incus --version) ready"`)
	return strings.Join(lines, "\n")
}
