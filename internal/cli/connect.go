package cli

import (
	"net"
	"time"

	"github.com/spf13/cobra"
)

func newConnectCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var useVM bool
	command := &cobra.Command{
		Use:     "connect [[dns-suffix:]project:]machine [-- command...]",
		Aliases: []string{"c"},
		Short:   "Connect to a Sandcastle machine",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			summary, err := requireV2Tenant(cmd.Context(), config)
			if err != nil {
				return err
			}
			// Cross-install (ADR-0020): if the reference names another install by
			// its DNS suffix, switch to that install's remote (and re-fetch its
			// summary) before connecting. A same-install reference falls through
			// unchanged.
			if dnsSuffix, project, machine, perr := parseV2MachineReference(args[0], summary.Tenant, config.adminConfig.Project); perr == nil {
				switchTo, terr := resolveConnectTarget(dnsSuffix, project, summary.DNSSuffix, localRemoteExists)
				if terr != nil {
					return terr
				}
				if switchTo != "" {
					switched := switchConfigToRemote(config, switchTo)
					targetSummary, err := requireV2Tenant(cmd.Context(), switched)
					if err != nil {
						return err
					}
					return runConnectV2(cmd.Context(), switched, targetSummary, project+":"+machine, args[1:], useVM)
				}
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
