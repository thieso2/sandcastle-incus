package cli

import "github.com/spf13/cobra"

// newSSHKeyCommand groups SSH-key maintenance verbs. Under v2 the developer key
// is published through the project default profile at login, so the v1 `set`
// verb is gone; the remaining user-facing verb is `purge`, which reconciles
// ~/.ssh/known_hosts against the tenant's live machines (ADR-0020).
func newSSHKeyCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "ssh-key",
		Short: "Manage Sandcastle SSH host-key entries in ~/.ssh/known_hosts",
	}
	command.AddCommand(newSSHKeyPurgeCommand(config, opts))
	return command
}
