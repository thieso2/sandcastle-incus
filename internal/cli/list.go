package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type listPayload struct {
	Projects []project.Summary `json:"projects"`
}

func newListCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List Sandcastle projects",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			projects, err := project.List(cmd.Context(), config.projectStore)
			if err != nil {
				return err
			}
			payload := listPayload{Projects: projects}
			return writeOutput(config.stdout, opts.output, formatProjectList(projects), payload)
		},
	}
}

func formatProjectList(projects []project.Summary) string {
	if len(projects) == 0 {
		return "No Sandcastle projects found."
	}

	var builder strings.Builder
	for _, project := range projects {
		fmt.Fprintf(
			&builder,
			"%s/%s\t%s\t%s\n",
			project.Owner,
			project.Name,
			project.Domain,
			project.Status,
		)
	}
	return strings.TrimRight(builder.String(), "\n")
}
