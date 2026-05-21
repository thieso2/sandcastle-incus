package cli

import (
	"fmt"
	"strings"

	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
)

func formatSandboxInspect(result sandbox.InspectResult) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Machine: %s/%s/%s\n", result.Tenant.Tenant, result.Project, result.Name)
	fmt.Fprintf(&builder, "Incus project: %s\n", result.Tenant.IncusName)
	fmt.Fprintf(&builder, "Instance: %s\n", result.InstanceName)
	fmt.Fprintf(&builder, "Private IP: %s\n", result.Machine.PrivateIP)
	fmt.Fprintf(&builder, "App port: %d\n", result.Machine.AppPort)
	fmt.Fprintf(&builder, "Linux user: %s\n", result.Machine.LinuxUser)
	fmt.Fprintf(&builder, "Home dir: %s\n", result.Machine.HomeDir)
	fmt.Fprintf(&builder, "Workspace dir: %s\n", result.Machine.WorkspaceDir)
	fmt.Fprintf(&builder, "Container tools: %s\n", enabledString(result.Machine.ContainerTools))
	if result.Machine.Running {
		fmt.Fprintln(&builder, "State: running")
	} else {
		fmt.Fprintln(&builder, "State: stopped")
	}
	if len(result.Machine.ExtraSANs) > 0 {
		fmt.Fprintf(&builder, "Extra SANs: %s\n", strings.Join(result.Machine.ExtraSANs, ","))
	}
	return strings.TrimRight(builder.String(), "\n")
}
