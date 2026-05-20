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
	var token string
	cmd := &cobra.Command{
		Use:   "add <name> <addr>",
		Short: "Add a Sandcastle remote and configure its Incus connection",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, addr := args[0], args[1]
			incusDir := scconfig.RemoteIncusDir(name)
			if err := os.MkdirAll(incusDir, 0o700); err != nil {
				return fmt.Errorf("create incus config dir: %w", err)
			}

			incusArgs := []string{"remote", "add", name, addr}
			if token != "" {
				incusArgs = append(incusArgs, "--token", token)
			}
			incusCmd := exec.CommandContext(cmd.Context(), "incus", incusArgs...)
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
	cmd.Flags().StringVar(&token, "token", "", "Incus trust token for authenticating with the remote server")
	return cmd
}
