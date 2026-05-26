package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
)

func newCacheCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage the local Sandcastle connect cache",
	}
	cmd.AddCommand(newCacheClearCommand(config, opts))
	return cmd
}

func newCacheClearCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "clear [tenant]",
		Short: "Clear cached SSH connect plans and keyscans for a tenant (or all tenants)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cache := incusx.NewConnectCache(config.adminConfig.Remote)
			if len(args) == 0 {
				cache.InvalidateAll()
				fmt.Fprintln(config.stdout, "Connect cache cleared.")
				return nil
			}
			t := args[0]
			cache.InvalidateTenant(t)
			fmt.Fprintf(config.stdout, "Connect cache cleared for tenant %s.\n", t)
			return nil
		},
	}
}
