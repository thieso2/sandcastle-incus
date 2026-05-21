package machine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type resolvedMachine struct {
	Summary      project.Summary
	Project      string
	Name         string
	InstanceName string
}

type AmbiguousMachineError struct {
	Name     string
	Projects []string
}

func (e AmbiguousMachineError) Error() string {
	return fmt.Sprintf("Sandcastle machine %s is ambiguous across projects %s; use project/machine", e.Name, strings.Join(e.Projects, ", "))
}

func IsAmbiguousMachineError(err error) bool {
	var ambiguous AmbiguousMachineError
	return errors.As(err, &ambiguous)
}

func resolveExistingMachine(ctx context.Context, admin config.Admin, projectStore project.IncusProjectStore, machineStore Store, reference string) (resolvedMachine, error) {
	tenantRef, err := currentTenantRef(admin)
	if err != nil {
		return resolvedMachine{}, err
	}
	summary, err := findTenant(ctx, projectStore, tenantRef)
	if err != nil {
		return resolvedMachine{}, err
	}
	projectRef, machineName, err := naming.ParseUserMachineRef(reference, admin.Project)
	if err != nil {
		return resolvedMachine{}, err
	}
	if strings.Contains(reference, "/") || strings.TrimSpace(admin.Project) != "" {
		return resolveKnownProjectMachine(summary, projectRef.Project, machineName)
	}
	if machineStore == nil {
		return resolveKnownProjectMachine(summary, projectRef.Project, machineName)
	}
	machines, err := listExistingMachines(ctx, machineStore, summary)
	if err != nil {
		return resolvedMachine{}, err
	}
	matches := []string{}
	for _, machine := range machines {
		if machine.Name == machineName {
			matches = append(matches, machine.Project)
		}
	}
	switch len(matches) {
	case 0:
		return resolvedMachine{}, fmt.Errorf("Sandcastle machine %s not found", reference)
	case 1:
		return resolveKnownProjectMachine(summary, matches[0], machineName)
	default:
		return resolvedMachine{}, AmbiguousMachineError{Name: machineName, Projects: matches}
	}
}

func resolveKnownProjectMachine(summary project.Summary, projectName string, machineName string) (resolvedMachine, error) {
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
	}, nil
}
