package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// infoOutput is the machine-readable form of `sc info` (`--json`).
type infoOutput struct {
	Tenant       string   `json:"tenant"`
	Project      string   `json:"project"`
	Remote       string   `json:"remote"`
	AuthHostname string   `json:"authHostname,omitempty"`
	Projects     []string `json:"projects,omitempty"`
}

func newInfoCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show the active Sandcastle context (tenant, project, remote) and available projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := infoOutput{
				Tenant:       config.adminConfig.Tenant,
				Project:      config.adminConfig.Project,
				Remote:       config.adminConfig.Remote,
				AuthHostname: commandAuthHostname(config, ""),
			}
			current := strings.TrimSpace(config.adminConfig.Project)
			note := ""
			// Fetch the tenant's projects live so the user can see valid names.
			// Degrade to config-only when offline/unresolved — `sc info` must not
			// fail just because the tenant can't be reached.
			if summary, ok, _ := v2TenantSummary(cmd.Context(), config); ok {
				for _, p := range summary.Projects {
					info.Projects = append(info.Projects, p.Name)
				}
				if current == "" {
					current = strings.TrimSpace(summary.DefaultProject)
				}
			} else if strings.TrimSpace(info.Tenant) != "" {
				note = "(could not reach tenant; showing local config only)"
			}
			return writeOutput(config.stdout, opts.output, formatInfo(info, current, note), info)
		},
	}
}

func formatInfo(info infoOutput, current string, note string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Tenant:   %s\n", infoValueOrUnset(info.Tenant))
	fmt.Fprintf(&b, "Project:  %s\n", infoValueOrUnset(info.Project))
	fmt.Fprintf(&b, "Remote:   %s\n", infoValueOrUnset(info.Remote))
	if strings.TrimSpace(info.AuthHostname) != "" {
		fmt.Fprintf(&b, "Auth:     %s\n", info.AuthHostname)
	}
	if len(info.Projects) > 0 {
		fmt.Fprintf(&b, "\nProjects in %s:\n", info.Tenant)
		for _, p := range info.Projects {
			marker := "    "
			if p == current {
				marker = "  * "
			}
			fmt.Fprintf(&b, "%s%s\n", marker, p)
		}
	} else if note != "" {
		fmt.Fprintf(&b, "\n%s\n", note)
	}
	return strings.TrimRight(b.String(), "\n")
}

func infoValueOrUnset(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(unset)"
	}
	return value
}
