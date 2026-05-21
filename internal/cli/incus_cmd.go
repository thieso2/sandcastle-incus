package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
)

type incusRunner func(context.Context, []string, []string, io.Reader, io.Writer, io.Writer) error

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
			runner := config.incusRunner
			if runner == nil {
				runner = runIncusCLI
			}
			return runner(cmd.Context(), args, append(os.Environ(), "INCUS_CONF="+incusDir), config.stdin, config.stdout, config.stderr)
		},
	}
}

func runIncusCLI(ctx context.Context, args []string, env []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	incusCmd := exec.CommandContext(ctx, "incus", args...)
	incusCmd.Env = env
	incusCmd.Stdin = stdin
	incusCmd.Stdout = stdout
	incusCmd.Stderr = stderr
	return incusCmd.Run()
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
