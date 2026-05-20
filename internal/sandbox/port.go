package sandbox

import (
	"context"
	"fmt"

	"github.com/thieso2/sandcastle-incus/internal/caddy"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type PortSetRequest struct {
	Reference string
	AppPort   int
}

type PortSetPlan struct {
	Reference    string          `json:"reference"`
	Project      project.Summary `json:"project"`
	Name         string          `json:"name"`
	InstanceName string          `json:"instanceName"`
	AppPort      int             `json:"appPort"`
	CaddyFile    caddy.File      `json:"caddyFile"`
}

type PortSetter interface {
	SetAppPort(context.Context, PortSetPlan) error
}

func PlanSetPort(ctx context.Context, admin config.Admin, store project.IncusProjectStore, request PortSetRequest) (PortSetPlan, error) {
	if err := admin.Validate(); err != nil {
		return PortSetPlan{}, err
	}
	if request.AppPort < 1 || request.AppPort > 65535 {
		return PortSetPlan{}, fmt.Errorf("invalid app port %d", request.AppPort)
	}
	projectRef, sandboxName, err := parseSandboxRef(request.Reference, admin.Owner)
	if err != nil {
		return PortSetPlan{}, err
	}
	summary, err := findProject(ctx, store, projectRef)
	if err != nil {
		return PortSetPlan{}, err
	}
	return PortSetPlan{
		Reference:    request.Reference,
		Project:      summary,
		Name:         sandboxName,
		InstanceName: "sc-" + sandboxName,
		AppPort:      request.AppPort,
		CaddyFile:    caddy.RenderSandbox(sandboxName+"."+summary.Domain, request.AppPort, SandboxCertPath, SandboxCertKeyPath),
	}, nil
}
