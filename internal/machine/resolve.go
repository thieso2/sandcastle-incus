package machine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type resolvedMachine struct {
	Summary      tenant.Summary
	Project      string
	Name         string
	InstanceName string
	PrivateIP    string
	Managed      bool
}

type AmbiguousMachineError struct {
	Name     string
	Projects []string
}

func (e AmbiguousMachineError) Error() string {
	return fmt.Sprintf("Sandcastle machine %s is ambiguous across projects %s; use project:machine or project/machine", e.Name, strings.Join(e.Projects, ", "))
}

func IsAmbiguousMachineError(err error) bool {
	var ambiguous AmbiguousMachineError
	return errors.As(err, &ambiguous)
}

func resolveExistingMachine(ctx context.Context, admin config.Admin, tenantStore tenant.IncusTenantStore, machineStore Store, reference string) (resolvedMachine, error) {
	tenantRef, err := currentTenantRef(admin)
	if err != nil {
		return resolvedMachine{}, err
	}
	summary, err := findTenant(ctx, tenantStore, tenantRef)
	if err != nil {
		return resolvedMachine{}, err
	}
	if resolved, ok, err := resolveMachineFQDN(ctx, machineStore, summary, reference); ok || err != nil {
		return resolved, err
	}
	projectRef, machineName, err := naming.ParseUserMachineRef(reference, admin.Project)
	if err != nil {
		if unmanaged, unmanagedErr := resolveUnmanagedMachine(ctx, machineStore, summary, reference); unmanagedErr == nil {
			return unmanaged, nil
		}
		return resolvedMachine{}, err
	}
	if machineStore == nil {
		return resolveKnownProjectMachine(summary, projectRef.Project, machineName)
	}
	machines, err := listExistingMachines(ctx, machineStore, summary)
	if err != nil {
		return resolvedMachine{}, err
	}
	if isExplicitProjectMachineRef(reference) || strings.TrimSpace(admin.Project) != "" {
		for _, machine := range machines {
			if machine.Project == projectRef.Project && machine.Name == machineName {
				return resolveKnownProjectMachineWithIP(summary, projectRef.Project, machineName, machine.PrivateIP)
			}
		}
		return resolvedMachine{}, fmt.Errorf("Sandcastle machine %s not found", reference)
	}
	matches := []string{}
	matchIPs := map[string]string{}
	for _, machine := range machines {
		if machine.Name == machineName {
			matches = append(matches, machine.Project)
			matchIPs[machine.Project] = machine.PrivateIP
		}
	}
	switch len(matches) {
	case 0:
		if unmanaged, err := resolveUnmanagedMachine(ctx, machineStore, summary, reference); err == nil {
			return unmanaged, nil
		}
		return resolvedMachine{}, fmt.Errorf("Sandcastle machine %s not found", reference)
	case 1:
		return resolveKnownProjectMachineWithIP(summary, matches[0], machineName, matchIPs[matches[0]])
	default:
		return resolvedMachine{}, AmbiguousMachineError{Name: machineName, Projects: matches}
	}
}

func isExplicitProjectMachineRef(reference string) bool {
	return strings.Contains(reference, "/") || strings.Contains(reference, ":")
}

func resolveMachineFQDN(ctx context.Context, machineStore Store, summary tenant.Summary, reference string) (resolvedMachine, bool, error) {
	if machineStore == nil || !strings.Contains(reference, ".") {
		return resolvedMachine{}, false, nil
	}
	hostname := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(reference)), ".")
	if hostname == "" {
		return resolvedMachine{}, false, nil
	}
	machines, err := listExistingMachines(ctx, machineStore, summary)
	if err != nil {
		return resolvedMachine{}, true, err
	}
	for _, machine := range machines {
		if strings.ToLower(machine.Name+"."+machine.Project+"."+summary.DNSSuffix) != hostname {
			continue
		}
		resolved, err := resolveKnownProjectMachineWithIP(summary, machine.Project, machine.Name, machine.PrivateIP)
		return resolved, true, err
	}
	return resolvedMachine{}, false, nil
}

func resolveKnownProjectMachine(summary tenant.Summary, projectName string, machineName string) (resolvedMachine, error) {
	return resolveKnownProjectMachineWithIP(summary, projectName, machineName, "")
}

func resolveKnownProjectMachineWithIP(summary tenant.Summary, projectName string, machineName string, privateIP string) (resolvedMachine, error) {
	if !tenantHasProject(summary, projectName) {
		return resolvedMachine{}, fmt.Errorf("Sandcastle project %s not found in tenant %s", projectName, summary.Tenant)
	}
	instanceName, err := naming.MachineIncusInstanceName(naming.MachineRef{Tenant: summary.Tenant, Project: projectName, Machine: machineName})
	if err != nil {
		return resolvedMachine{}, err
	}
	return resolvedMachine{
		Summary:      summary,
		Project:      projectName,
		Name:         machineName,
		InstanceName: instanceName,
		PrivateIP:    privateIP,
		Managed:      true,
	}, nil
}

func resolveUnmanagedMachine(ctx context.Context, machineStore Store, summary tenant.Summary, reference string) (resolvedMachine, error) {
	if machineStore == nil || strings.Contains(reference, "/") {
		return resolvedMachine{}, fmt.Errorf("unmanaged machine %s not found", reference)
	}
	unmanagedStore, ok := machineStore.(UnmanagedStore)
	if !ok {
		return resolvedMachine{}, fmt.Errorf("unmanaged machine %s not found", reference)
	}
	name := strings.TrimSpace(reference)
	if name == "" {
		return resolvedMachine{}, fmt.Errorf("unmanaged machine %s not found", reference)
	}
	machines, err := unmanagedStore.ListUnmanagedMachines(ctx, summary)
	if err != nil {
		return resolvedMachine{}, fmt.Errorf("list unmanaged machines for %s: %w", summary.Tenant, err)
	}
	for _, machine := range machines {
		if machine.Name != name && machine.InstanceName != name {
			continue
		}
		instanceName := machine.InstanceName
		if strings.TrimSpace(instanceName) == "" {
			instanceName = machine.Name
		}
		return resolvedMachine{
			Summary:      summary,
			Name:         machine.Name,
			InstanceName: instanceName,
			PrivateIP:    machine.PrivateIP,
			Managed:      false,
		}, nil
	}
	return resolvedMachine{}, fmt.Errorf("unmanaged machine %s not found", reference)
}
