package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
)

func newInspectCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "inspect project/name",
		Short: "Inspect a Sandcastle sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := sandbox.Inspect(
				cmd.Context(),
				config.adminConfig,
				config.projectStore,
				config.sandboxStore,
				sandbox.InspectRequest{Reference: args[0]},
			)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatSandboxInspect(result), result)
		},
	}
}

func formatSandboxInspect(result sandbox.InspectResult) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Sandbox: %s/%s/%s\n", result.Sandbox.Owner, result.Sandbox.Project, result.Sandbox.Name)
	fmt.Fprintf(&builder, "Incus project: %s\n", result.Project.IncusName)
	fmt.Fprintf(&builder, "Instance: %s\n", result.InstanceName)
	fmt.Fprintf(&builder, "Private IP: %s\n", result.Sandbox.PrivateIP)
	fmt.Fprintf(&builder, "App port: %d\n", result.Sandbox.AppPort)
	fmt.Fprintf(&builder, "Linux user: %s\n", result.Sandbox.LinuxUser)
	fmt.Fprintf(&builder, "Home dir: %s\n", result.Sandbox.HomeDir)
	fmt.Fprintf(&builder, "Workspace dir: %s\n", result.Sandbox.WorkspaceDir)
	fmt.Fprintf(&builder, "Container tools: %s\n", enabledString(result.Sandbox.ContainerTools))
	if result.Sandbox.Running {
		fmt.Fprintln(&builder, "State: running")
	} else {
		fmt.Fprintln(&builder, "State: stopped")
	}
	if len(result.Sandbox.ExtraSANs) > 0 {
		fmt.Fprintf(&builder, "Extra SANs: %s\n", strings.Join(result.Sandbox.ExtraSANs, ","))
	}
	return strings.TrimRight(builder.String(), "\n")
}
