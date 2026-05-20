package cli

import (
	"fmt"
	"os"
	"os/exec"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/spf13/cobra"
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
	return &cobra.Command{
		Use:   "add <name> <join-token>",
		Short: "Add a Sandcastle remote using an Incus join token",
		Long: `Add a Sandcastle remote.

<join-token> is the token produced by "sandcastle admin user create" (or
"incus config trust add --generate-certificate" on the server). The token
already contains the server address — no separate address argument is needed.

Incus certs are stored in ~/.config/sandcastle/<name>/incus/ and the remote
is saved as the default in ~/.config/sandcastle/config.yml.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, joinToken := args[0], args[1]
			incusDir := scconfig.RemoteIncusDir(name)
			if err := os.MkdirAll(incusDir, 0o700); err != nil {
				return fmt.Errorf("create incus config dir: %w", err)
			}

			// Pass the join token as the address argument — incus detects it is
			// a JSON token and extracts the server address from it automatically.
			incusCmd := exec.CommandContext(cmd.Context(), "incus", "remote", "add", name, joinToken)
			incusCmd.Env = append(os.Environ(), "INCUS_CONF="+incusDir)
			incusCmd.Stdin = config.stdin
			incusCmd.Stdout = config.stdout
			incusCmd.Stderr = config.stderr
			if err := incusCmd.Run(); err != nil {
				return fmt.Errorf("incus remote add: %w", err)
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
			if err := scconfig.SaveSandcastleConfig(cfgPath, cfg); err != nil {
				return fmt.Errorf("save sandcastle config: %w", err)
			}
			fmt.Fprintf(config.stdout, "Remote %q added. Incus config: %s\n", name, incusDir)
			return nil
		},
	}
}
