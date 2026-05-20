package cli

import (
	"fmt"
	"os"
	"os/exec"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/spf13/cobra"
)

func newIncusCommand(config commandConfig, _ *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:                "incus [args...]",
		Short:              "Run incus with the active Sandcastle remote's Incus config",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			incusDir := resolveIncusDir(config.adminConfig.Remote)
			if incusDir == "" {
				return fmt.Errorf("no Sandcastle-managed Incus config found for remote %q; add one with: sc remote add", config.adminConfig.Remote)
			}
			incusCmd := exec.CommandContext(cmd.Context(), "incus", args...)
			incusCmd.Env = append(os.Environ(), "INCUS_CONF="+incusDir)
			incusCmd.Stdin = config.stdin
			incusCmd.Stdout = config.stdout
			incusCmd.Stderr = config.stderr
			return incusCmd.Run()
		},
	}
}

// resolveIncusDir returns the per-remote Incus config dir if it exists, otherwise empty string.
func resolveIncusDir(remote string) string {
	if remote == "" {
		return ""
	}
	dir := scconfig.RemoteIncusDir(remote)
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	return ""
}
