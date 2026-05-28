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
	Summary       tenant.Summary
	Project       string
	Name          string
	InstanceName  string
	PrivateIP     string
	LinuxUser     string
	CloudIdentity string
	Running       bool
	Managed       bool
}

type AmbiguousMachineError struct {
	Name     string
	Projects []string
}

func (e AmbiguousMachineError) Error() string {
	return fmt.Sprintf("Sandcastle machine %s is ambiguous across projects %s; use project:machine or tenant/project:machine", e.Name, strings.Join(e.Projects, ", "))
}

func IsAmbiguousMachineError(err error) bool {
	var ambiguous AmbiguousMachineError
	return errors.As(err, &ambiguous)
}

func resolveExistingMachine(ctx context.Context, admin config.Admin, tenantStore tenant.IncusTenantStore, machineStore Store, reference string) (resolvedMachine, error) {
	target, targetErr := parseMachineTarget(ctx, admin, tenantStore, reference)
	summary := target.Summary
	if targetErr != nil {
		tenantRef, err := currentTenantRef(admin)
		if err == nil {
			if currentSummary, err := findTenant(ctx, tenantStore, tenantRef); err == nil {
				summary = currentSummary
			}
		}
	}
	if resolved, ok, err := resolveMachineFQDN(ctx, machineStore, summary, reference); ok || err != nil {
		return resolved, err
	}
	if targetErr != nil {
		if unmanaged, unmanagedErr := resolveUnmanagedMachine(ctx, machineStore, summary, reference); unmanagedErr == nil {
			return unmanaged, nil
		}
		return resolvedMachine{}, targetErr
	}
	if machineStore == nil {
		return resolveKnownProjectMachine(summary, target.Project, target.Name)
	}
	machines, err := listExistingMachines(ctx, machineStore, summary)
	if err != nil {
		return resolvedMachine{}, err
	}
	if target.ExplicitProject {
		for _, machine := range machines {
			if machine.Project == target.Project && machine.Name == target.Name {
				return resolveKnownProjectMachineWithMetadata(summary, target.Project, target.Name, machine.PrivateIP, machine.LinuxUser, machine.CloudIdentity, machine.Running)
			}
		}
		return resolvedMachine{}, fmt.Errorf("Sandcastle machine %s not found", reference)
	}
	matches := []string{}
	matchIPs := map[string]string{}
	matchUsers := map[string]string{}
	matchCloudIdentities := map[string]string{}
	matchRunning := map[string]bool{}
	for _, machine := range machines {
		if machine.Name == target.Name {
			matches = append(matches, machine.Project)
			matchIPs[machine.Project] = machine.PrivateIP
			matchUsers[machine.Project] = machine.LinuxUser
			matchCloudIdentities[machine.Project] = machine.CloudIdentity
			matchRunning[machine.Project] = machine.Running
		}
	}
	switch len(matches) {
	case 0:
		if unmanaged, err := resolveUnmanagedMachine(ctx, machineStore, summary, reference); err == nil {
			return unmanaged, nil
		}
		return resolvedMachine{}, fmt.Errorf("Sandcastle machine %s not found", reference)
	case 1:
		return resolveKnownProjectMachineWithMetadata(summary, matches[0], target.Name, matchIPs[matches[0]], matchUsers[matches[0]], matchCloudIdentities[matches[0]], matchRunning[matches[0]])
	default:
		return resolvedMachine{}, AmbiguousMachineError{Name: target.Name, Projects: matches}
	}
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
		resolved, err := resolveKnownProjectMachineWithMetadata(summary, machine.Project, machine.Name, machine.PrivateIP, machine.LinuxUser, machine.CloudIdentity, machine.Running)
		return resolved, true, err
	}
	return resolvedMachine{}, false, nil
}

func resolveKnownProjectMachine(summary tenant.Summary, projectName string, machineName string) (resolvedMachine, error) {
	return resolveKnownProjectMachineWithIP(summary, projectName, machineName, "")
}

func resolveKnownProjectMachineWithIP(summary tenant.Summary, projectName string, machineName string, privateIP string) (resolvedMachine, error) {
	return resolveKnownProjectMachineWithMetadata(summary, projectName, machineName, privateIP, "", "", false)
}

func resolveKnownProjectMachineWithMetadata(summary tenant.Summary, projectName string, machineName string, privateIP string, linuxUser string, cloudIdentity string, running bool) (resolvedMachine, error) {
	if !tenantHasProject(summary, projectName) {
		return resolvedMachine{}, fmt.Errorf("Sandcastle project %s not found in tenant %s", projectName, summary.Tenant)
	}
	instanceName, err := naming.MachineIncusInstanceName(naming.MachineRef{Tenant: summary.Tenant, Project: projectName, Machine: machineName})
	if err != nil {
		return resolvedMachine{}, err
	}
	return resolvedMachine{
		Summary:       summary,
		Project:       projectName,
		Name:          machineName,
		InstanceName:  instanceName,
		PrivateIP:     privateIP,
		LinuxUser:     strings.TrimSpace(linuxUser),
		CloudIdentity: strings.TrimSpace(cloudIdentity),
		Running:       running,
		Managed:       true,
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
