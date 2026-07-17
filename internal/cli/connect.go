package cli

import (
	"context"
	"net"
	"time"

	"github.com/spf13/cobra"

	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

// withResolvedV2Machine runs action against the install and summary that own the
// referenced machine, handling both the enrolled-remote prefix (rebind) and the
// cross-install DNS-suffix switch (ADR-0020). It is shared by `sc connect` and
// `sc fix` so both resolve `[[remote:]project:]machine` identically.
func withResolvedV2Machine(cmd *cobra.Command, config commandConfig, ref string, action func(ctx context.Context, config commandConfig, summary tenant.Summary, reference string) error) error {
	// Universal [[remote:]project:]machine: a leading enrolled-remote prefix
	// rebinds the whole command to that install (all stores); the reference
	// then continues as project:machine below.
	config, reference, restore, err := rebindForReference(config, ref)
	if err != nil {
		return err
	}
	defer restore()
	summary, err := requireV2Tenant(cmd.Context(), config)
	if err != nil {
		return err
	}
	// Cross-install (ADR-0020): if the reference names another install by its DNS
	// suffix, switch to that install's remote (and re-fetch its summary) first. A
	// same-install reference falls through unchanged.
	if dnsSuffix, project, machine, perr := parseV2MachineReference(reference, summary.Tenant, config.adminConfig.Project); perr == nil {
		switchTo, terr := resolveConnectTarget(dnsSuffix, summary.DNSSuffix, localRemoteExists)
		if terr != nil {
			return terr
		}
		if switchTo != "" {
			switched := switchConfigToRemote(config, switchTo, project)
			targetSummary, err := requireV2Tenant(cmd.Context(), switched)
			if err != nil {
				return err
			}
			return action(cmd.Context(), switched, targetSummary, project+":"+machine)
		}
	}
	return action(cmd.Context(), config, summary, reference)
}

func newConnectCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var useVM bool
	command := &cobra.Command{
		Use:     "connect [[remote:]project:]machine [-- command...]",
		Aliases: []string{"c"},
		Short:   "Connect to a Sandcastle machine",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withResolvedV2Machine(cmd, config, args[0], func(ctx context.Context, config commandConfig, summary tenant.Summary, reference string) error {
				return runConnectV2(ctx, config, summary, reference, args[1:], useVM)
			})
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
