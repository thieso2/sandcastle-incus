package cli

import "github.com/spf13/cobra"

type listPayload struct {
	Projects []projectSummary `json:"projects"`
}

type projectSummary struct {
	Owner  string `json:"owner"`
	Name   string `json:"name"`
	Domain string `json:"domain,omitempty"`
	Status string `json:"status"`
}

func newListCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List Sandcastle projects",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := listPayload{Projects: []projectSummary{}}
			return writeOutput(config.stdout, opts.output, "No Sandcastle projects found.", payload)
		},
	}
}
