package cli

import (
	"github.com/spf13/cobra"
)

func newCreateCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	var detach bool
	var image string
	var vm bool
	command := &cobra.Command{
		Use:   "create [[remote:]project:]machine",
		Short: "Create a Sandcastle container machine",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			config, reference, restore, err := rebindForReference(config, args[0])
			if err != nil {
				return err
			}
			defer restore()
			summary, err := requireV2Tenant(cmd.Context(), config)
			if err != nil {
				return err
			}
			return runCreateMachineV2(cmd.Context(), config, opts, summary, reference, createV2Options{
				Image:  image,
				VM:     vm,
				DryRun: dryRun,
			})
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the machine creation plan without creating a container")
	// --detach/--background are accepted no-ops: v2 machine creation never
	// attaches, and the e2e protocol passes --detach throughout.
	command.Flags().BoolVar(&detach, "detach", false, "deprecated no-op; machine creation never attaches")
	command.Flags().BoolVar(&detach, "background", false, "deprecated no-op; machine creation never attaches")
	command.Flags().StringVar(&image, "image", "", "image to launch (default "+v2DefaultMachineImage+")")
	command.Flags().BoolVar(&vm, "vm", false, "launch a virtual machine instead of a container")
	return command
}
