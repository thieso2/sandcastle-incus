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
			fmt.Fprintf(config.stdout, "\nresolved:\n")
			fmt.Fprintf(config.stdout, "  owner:      %q\n", config.adminConfig.Owner)
			fmt.Fprintf(config.stdout, "  remote:     %q\n", config.adminConfig.Remote)
			fmt.Fprintf(config.stdout, "  configPath: %q\n", config.adminConfig.ConfigPath)
			return nil
		},
	}
}
