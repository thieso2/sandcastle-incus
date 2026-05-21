package machine

import (
	"context"
	"fmt"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type Action string

const (
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
	ActionRemove  Action = "remove"
)

type LifecycleRequest struct {
	Reference string
	Action    Action
}

type LifecyclePlan struct {
	Reference    string          `json:"reference"`
	Tenant       project.Summary `json:"tenant"`
	Project      string          `json:"project"`
	Name         string          `json:"name"`
	InstanceName string          `json:"instanceName"`
	Action       Action          `json:"action"`
}

type Controller interface {
	ApplyLifecycle(context.Context, LifecyclePlan) error
}

func PlanLifecycle(ctx context.Context, admin config.Admin, store project.IncusProjectStore, request LifecycleRequest) (LifecyclePlan, error) {
	if err := admin.Validate(); err != nil {
		return LifecyclePlan{}, err
	}
	if err := validateAction(request.Action); err != nil {
		return LifecyclePlan{}, err
	}
	tenantRef, err := currentTenantRef(admin)
	if err != nil {
		return LifecyclePlan{}, err
	}
	projectRef, machineName, err := naming.ParseUserMachineRef(request.Reference, admin.Project)
	if err != nil {
		return LifecyclePlan{}, err
	}
	summary, err := findTenant(ctx, store, tenantRef)
	if err != nil {
		return LifecyclePlan{}, err
	}
	if !tenantHasProject(summary, projectRef.Project) {
		return LifecyclePlan{}, fmt.Errorf("Sandcastle project %s not found in tenant %s", projectRef.Project, summary.Tenant)
	}
	instanceName, err := naming.MachineIncusInstanceName(naming.MachineRef{Tenant: summary.Tenant, Project: projectRef.Project, Machine: machineName})
	if err != nil {
		return LifecyclePlan{}, err
	}
	return LifecyclePlan{
		Reference:    request.Reference,
		Tenant:       summary,
		Project:      projectRef.Project,
		Name:         machineName,
		InstanceName: instanceName,
		Action:       request.Action,
	}, nil
}

func validateAction(action Action) error {
	switch action {
	case ActionStart, ActionStop, ActionRestart, ActionRemove:
		return nil
	default:
		return fmt.Errorf("unsupported machine action %q", action)
	}
}
