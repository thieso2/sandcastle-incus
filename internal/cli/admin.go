package cli

import "github.com/spf13/cobra"

func newAdminCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	admin := &cobra.Command{
		Use:   "admin",
		Short: "Run Sandcastle administrator commands",
	}
	admin.AddCommand(newAdminVersionCommand(config, opts))
	admin.AddCommand(newAdminProjectCommand(config, opts))
	return admin
}

func newAdminVersionCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Sandcastle admin command version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := versionPayload{Name: config.name, Version: version}
			return writeOutput(config.stdout, opts.output, version, payload)
		},
	}
}

func newAdminProjectCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	project := &cobra.Command{
		Use:   "project",
		Short: "Manage Sandcastle projects",
	}
	project.AddCommand(newAdminProjectListCommand(config, opts))
	return project
}

func newAdminProjectListCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Sandcastle-managed Incus projects",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			projects, err := listProjects(cmd.Context(), config.projectStore)
			if err != nil {
				return err
			}
			payload := listPayload{Projects: projects}
			return writeOutput(config.stdout, opts.output, formatProjectList(projects), payload)
		},
	}
}
