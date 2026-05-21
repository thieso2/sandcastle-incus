package machine

import (
	"context"
	"fmt"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type InspectRequest struct {
	Reference string
}

type InspectResult struct {
	Reference    string          `json:"reference"`
	Tenant       project.Summary `json:"tenant"`
	Project      string          `json:"project"`
	Name         string          `json:"name"`
	InstanceName string          `json:"instanceName"`
	Machine      meta.Machine    `json:"machine"`
}

func Inspect(ctx context.Context, admin config.Admin, projectStore project.IncusProjectStore, sandboxStore Store, request InspectRequest) (InspectResult, error) {
	if err := admin.Validate(); err != nil {
		return InspectResult{}, err
	}
	tenantRef, err := currentTenantRef(admin)
	if err != nil {
		return InspectResult{}, err
	}
	projectRef, machineName, err := naming.ParseUserMachineRef(request.Reference, admin.Project)
	if err != nil {
		return InspectResult{}, err
	}
	summary, err := findTenant(ctx, projectStore, tenantRef)
	if err != nil {
		return InspectResult{}, err
	}
	machines, err := listExistingMachines(ctx, sandboxStore, summary)
	if err != nil {
		return InspectResult{}, err
	}
	for _, machine := range machines {
		if machine.Project == projectRef.Project && machine.Name == machineName {
			instanceName, err := naming.MachineIncusInstanceName(naming.MachineRef{Tenant: summary.Tenant, Project: projectRef.Project, Machine: machineName})
			if err != nil {
				return InspectResult{}, err
			}
			return InspectResult{
				Reference:    request.Reference,
				Tenant:       summary,
				Project:      projectRef.Project,
				Name:         machineName,
				InstanceName: instanceName,
				Machine:      machine,
			}, nil
		}
	}
	return InspectResult{}, fmt.Errorf("Sandcastle machine %s not found", request.Reference)
}
