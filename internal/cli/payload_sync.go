package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// sc payload-sync: the tenant self-service /.sc platform-payload sync
// (ADR-0022) — the same per-project central write the admin's
// `sc-adm tenant payload-sync` performs, but scoped to the projects the
// tenant's own restricted certificate can see. No tenant argument, no install
// prefix: the current tenant and remote resolve from the tenant's login.
func newPayloadSyncCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var checkOnly bool
	command := &cobra.Command{
		Use:   "payload-sync",
		Short: "Converge your tenant's /.sc platform payload (once per project, not per machine)",
		Long: `Converge every app project of your current tenant onto the /.sc platform
payload built into this binary. Running machines pick the change up through
their shared /.sc mount — no re-create, no per-machine sweep; the guarded
shims apply it on next use. Your restricted certificate may perform the sync:
the volume lives inside your tenant's own projects. With --check it only
reports each project's payload version against this binary's (drift
detection). Rolling back = running payload-sync from the previous binary.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tenantName := strings.TrimSpace(config.adminConfig.Tenant)
			if tenantName == "" {
				return fmt.Errorf("tenant is required; set SANDCASTLE_TENANT or local tenant config")
			}
			statuses, err := config.tenantCreator.SyncVisiblePlatformPayload(cmd.Context(), tenantName, checkOnly)
			if err != nil {
				return err
			}
			lines := make([]string, 0, len(statuses))
			for _, s := range statuses {
				lines = append(lines, s.IncusProject+": "+formatSCPayloadStatus(s))
			}
			return writeOutput(config.stdout, opts.output, strings.Join(lines, "\n"), payloadSyncPayload{Tenant: tenantName, Projects: statuses})
		},
	}
	command.Flags().BoolVar(&checkOnly, "check", false, "report each project's payload version without writing anything")
	return command
}
