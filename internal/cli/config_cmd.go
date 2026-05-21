package cli

import (
	"fmt"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCommand(config commandConfig, _ *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or modify Sandcastle configuration",
	}
	cmd.AddCommand(newConfigShowCommand(config))
	cmd.AddCommand(newConfigSetCommand(config))
	return cmd
}

func newConfigShowCommand(config commandConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the resolved Sandcastle configuration",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgPath := scconfig.DefaultConfigPath()
			fileCfg, err := scconfig.LoadSandcastleConfig(cfgPath)
			if err != nil {
				fmt.Fprintf(config.stderr, "warning: could not read %s: %v\n", cfgPath, err)
			}
			fmt.Fprintf(config.stdout, "config file:  %s\n", cfgPath)
			fmt.Fprintf(config.stdout, "  file.owner:  %q\n", fileCfg.Owner)
			fmt.Fprintf(config.stdout, "  file.remote: %q\n", fileCfg.Remote)
			userPath := scconfig.ResolveConfigPath(config.adminConfig.Remote)
			fmt.Fprintf(config.stdout, "\nresolved:\n")
			fmt.Fprintf(config.stdout, "  owner:        %q\n", config.adminConfig.Owner)
			fmt.Fprintf(config.stdout, "  remote:       %q\n", config.adminConfig.Remote)
			fmt.Fprintf(config.stdout, "  admin_remote: %q  (used by sc admin; SANDCASTLE_ADMIN_REMOTE overrides)\n", config.adminConfig.AdminRemote)
			fmt.Fprintf(config.stdout, "  user incus config:  %q\n", userPath)
			fmt.Fprintf(config.stdout, "  admin incus config: ~/.config/incus/ (global default)\n")
			return nil
		},
	}
}

func newConfigSetCommand(_ commandConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a value in ~/.config/sandcastle/config.yml",
		Long: `Set a configuration value. Supported keys:
  owner         default owner name (e.g. alice)
  remote        default Sandcastle user remote name (e.g. sc-alice)
  admin_remote  Incus remote for sc admin commands in global ~/.config/incus/ (e.g. big)`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			cfgPath := scconfig.DefaultConfigPath()
			cfg, err := scconfig.LoadSandcastleConfig(cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			switch key {
			case "owner":
				cfg.Owner = value
			case "remote":
				cfg.Remote = value
			case "admin_remote":
				cfg.AdminRemote = value
			default:
				return fmt.Errorf("unknown config key %q; supported keys: owner, remote, admin_remote", key)
			}
			if err := scconfig.SaveSandcastleConfig(cfgPath, cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Set %s = %q in %s\n", key, value, cfgPath)
			return nil
		},
	}
}
