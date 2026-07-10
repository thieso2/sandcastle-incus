package cli

import (
	"net"
	"time"

	"github.com/spf13/cobra"
)

func newConnectCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var useVM bool
	command := &cobra.Command{
		Use:     "connect [tenant/][project:]machine [-- command...]",
		Aliases: []string{"c"},
		Short:   "Connect to a Sandcastle machine",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			summary, err := requireV2Tenant(cmd.Context(), config)
			if err != nil {
				return err
			}
			return runConnectV2(cmd.Context(), config, summary, args[0], args[1:], useVM)
		},
	}
	command.Flags().BoolVar(&useVM, "vm", false, "when the machine has to be created first, launch a virtual machine instead of a container")
	return command
}

// probeSSHPort reports whether the machine is accepting SSH yet. Kept from the
// deleted v1 connect path: runConnectV2 waits on it after creating a machine.
func probeSSHPort(host string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "22"), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
