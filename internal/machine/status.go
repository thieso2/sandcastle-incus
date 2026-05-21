package machine

import (
	"context"
	"fmt"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type StatusRequest struct {
	Reference string
}

type StatusResult struct {
	Reference    string         `json:"reference"`
	Tenant       tenant.Summary `json:"tenant"`
	Project      string         `json:"project"`
	Name         string         `json:"name"`
	InstanceName string         `json:"instanceName"`
	Machine      meta.Machine   `json:"machine"`
}

func GetStatus(ctx context.Context, admin config.Admin, tenantStore tenant.IncusTenantStore, machineStore Store, request StatusRequest) (StatusResult, error) {
	if err := admin.Validate(); err != nil {
		return StatusResult{}, err
	}
	resolved, err := resolveExistingMachine(ctx, admin, tenantStore, machineStore, request.Reference)
	if err != nil {
		return StatusResult{}, err
	}
	machines, err := listExistingMachines(ctx, machineStore, resolved.Summary)
	if err != nil {
		return StatusResult{}, err
	}
	for _, machine := range machines {
		if machine.Project == resolved.Project && machine.Name == resolved.Name {
			return StatusResult{
				Reference:    request.Reference,
				Tenant:       resolved.Summary,
				Project:      resolved.Project,
				Name:         resolved.Name,
				InstanceName: resolved.InstanceName,
				Machine:      machine,
			}, nil
		}
	}
	return StatusResult{}, fmt.Errorf("Sandcastle machine %s not found", request.Reference)
}
