package sandbox

import (
	"context"
	"fmt"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type InspectRequest struct {
	Reference string
}

type InspectResult struct {
	Reference    string          `json:"reference"`
	Project      project.Summary `json:"project"`
	Name         string          `json:"name"`
	InstanceName string          `json:"instanceName"`
	Sandbox      meta.Sandbox    `json:"sandbox"`
}

func Inspect(ctx context.Context, admin config.Admin, projectStore project.IncusProjectStore, sandboxStore Store, request InspectRequest) (InspectResult, error) {
	if err := admin.Validate(); err != nil {
		return InspectResult{}, err
	}
	projectRef, sandboxName, err := parseSandboxRef(request.Reference, admin.Owner)
	if err != nil {
		return InspectResult{}, err
	}
	summary, err := findProject(ctx, projectStore, projectRef)
	if err != nil {
		return InspectResult{}, err
	}
	sandboxes, err := listExistingSandboxes(ctx, sandboxStore, summary)
	if err != nil {
		return InspectResult{}, err
	}
	for _, sandbox := range sandboxes {
		if sandbox.Name == sandboxName {
			return InspectResult{
				Reference:    request.Reference,
				Project:      summary,
				Name:         sandboxName,
				InstanceName: "sc-" + sandboxName,
				Sandbox:      sandbox,
			}, nil
		}
	}
	return InspectResult{}, fmt.Errorf("Sandcastle sandbox %s not found", request.Reference)
}
