package sandbox

import (
	"context"
	"fmt"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/project"
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
	Project      project.Summary `json:"project"`
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
	projectRef, sandboxName, err := parseSandboxRef(request.Reference, admin.Owner)
	if err != nil {
		return LifecyclePlan{}, err
	}
	summary, err := findProject(ctx, store, projectRef)
	if err != nil {
		return LifecyclePlan{}, err
	}
	return LifecyclePlan{
		Reference:    request.Reference,
		Project:      summary,
		Name:         sandboxName,
		InstanceName: "sc-" + sandboxName,
		Action:       request.Action,
	}, nil
}

func validateAction(action Action) error {
	switch action {
	case ActionStart, ActionStop, ActionRestart, ActionRemove:
		return nil
	default:
		return fmt.Errorf("unsupported sandbox action %q", action)
	}
}
