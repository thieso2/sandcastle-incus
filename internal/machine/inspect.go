package machine

import (
	"context"
	"fmt"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
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
	resolved, err := resolveExistingMachine(ctx, admin, projectStore, sandboxStore, request.Reference)
	if err != nil {
		return InspectResult{}, err
	}
	machines, err := listExistingMachines(ctx, sandboxStore, resolved.Summary)
	if err != nil {
		return InspectResult{}, err
	}
	for _, machine := range machines {
		if machine.Project == resolved.Project && machine.Name == resolved.Name {
			return InspectResult{
				Reference:    request.Reference,
				Tenant:       resolved.Summary,
				Project:      resolved.Project,
				Name:         resolved.Name,
				InstanceName: resolved.InstanceName,
				Machine:      machine,
			}, nil
		}
	}
	return InspectResult{}, fmt.Errorf("Sandcastle machine %s not found", request.Reference)
}
