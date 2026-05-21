package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
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
	var owner string
	cmd := &cobra.Command{
		Use:   "add <name> <join-token>",
		Short: "Add a Sandcastle remote using an Incus join token",
		Long: `Add a Sandcastle remote.

<join-token> is the token produced by "sc admin user create" (or
"incus config trust add --generate-certificate" on the server). The token
already contains the server address — no separate address argument is needed.

Incus certs are stored in ~/.config/sandcastle/<name>/incus/ and the remote
is saved as the default in ~/.config/sandcastle/config.yml.

Use --owner to also set your default owner name in one step:

  sc remote add sc-alice JOIN_TOKEN --owner alice`,
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

			cfgPath := scconfig.DefaultConfigPath()
			cfg, err := scconfig.LoadSandcastleConfig(cfgPath)
			if err != nil {
				return fmt.Errorf("load sandcastle config: %w", err)
			}
			if cfg.Remote == "" {
				cfg.Remote = name
				fmt.Fprintf(config.stdout, "Default remote set to %q\n", name)
			}
			if owner != "" {
				cfg.Tenant = owner
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
	cmd.Flags().StringVar(&owner, "tenant", "", "Set the default tenant name in ~/.config/sandcastle/config.yml")
	return cmd
}
