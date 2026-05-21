package machine

import (
	"context"
	"fmt"

	"github.com/thieso2/sandcastle-incus/internal/config"
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

func PlanLifecycle(ctx context.Context, admin config.Admin, store project.IncusProjectStore, machineStore Store, request LifecycleRequest) (LifecyclePlan, error) {
	if err := admin.Validate(); err != nil {
		return LifecyclePlan{}, err
	}
	if err := validateAction(request.Action); err != nil {
		return LifecyclePlan{}, err
	}
	resolved, err := resolveExistingMachine(ctx, admin, store, machineStore, request.Reference)
	if err != nil {
		return LifecyclePlan{}, err
	}
	return LifecyclePlan{
		Reference:    request.Reference,
		Tenant:       resolved.Summary,
		Project:      resolved.Project,
		Name:         resolved.Name,
		InstanceName: resolved.InstanceName,
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
