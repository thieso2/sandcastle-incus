package cli

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
)

type payloadSyncPayload struct {
	Tenant   string                          `json:"tenant"`
	Projects []incusx.SCPayloadProjectStatus `json:"projects"`
}

// sc-adm tenant payload-sync: the central /.sc platform-payload update (#131,
// ADR-0022). One volume write per app project — never per machine — and every
// running machine in the tenant observes the new payload through its shared
// /.sc mount; the guarded shims pick it up on next use. Rollback = run the
// previous binary's payload-sync.
func newAdminTenantPayloadSyncCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var checkOnly bool
	command := &cobra.Command{
		Use:   "payload-sync tenant",
		Short: "Write the /.sc platform payload centrally (once per project, not per machine)",
		Long: `Converge every app project of a tenant onto the /.sc platform payload built
into this binary. Running machines pick the change up through their shared
/.sc mount — no re-create, no per-machine sweep. With --check it only reports
each project's payload version against this binary's (drift detection).
Rolling back = running payload-sync from the previous binary.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tenantName := strings.TrimSpace(args[0])
			statuses, err := config.tenantCreator.SyncTenantPlatformPayload(cmd.Context(), config.adminConfig.IncusProjectPrefix, tenantName, checkOnly)
			if err != nil {
				return err
			}
			lines := make([]string, 0, len(statuses)+1)
			for _, s := range statuses {
				before := s.Before
				if before == "" {
					before = "(none)"
				}
				var state string
				switch {
				case s.Changed:
					state = "synced " + before + " -> " + s.Target
				case s.Before == s.Target:
					state = "current (" + s.Target + ")"
				default:
					state = "STALE " + before + " (binary ships " + s.Target + ")"
				}
				lines = append(lines, s.IncusProject+": "+state)
			}
			return writeOutput(config.stdout, opts.output, strings.Join(lines, "\n"), payloadSyncPayload{Tenant: tenantName, Projects: statuses})
		},
	}
	command.Flags().BoolVar(&checkOnly, "check", false, "report each project's payload version without writing anything")
	return command
}
