package machine

import (
	"context"
	"fmt"

	"github.com/thieso2/sandcastle-incus/internal/caddy"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type PortSetRequest struct {
	Reference string
	AppPort   int
}

type PortSetPlan struct {
	Reference    string         `json:"reference"`
	Tenant       tenant.Summary `json:"tenant"`
	Project      string         `json:"project"`
	Name         string         `json:"name"`
	InstanceName string         `json:"instanceName"`
	AppPort      int            `json:"appPort"`
	CaddyFile    caddy.File     `json:"caddyFile"`
}

type PortSetter interface {
	SetAppPort(context.Context, PortSetPlan) error
}

func PlanSetPort(ctx context.Context, admin config.Admin, store tenant.IncusTenantStore, request PortSetRequest) (PortSetPlan, error) {
	if err := admin.Validate(); err != nil {
		return PortSetPlan{}, err
	}
	if request.AppPort < 1 || request.AppPort > 65535 {
		return PortSetPlan{}, fmt.Errorf("invalid app port %d", request.AppPort)
	}
	target, err := parseMachineTarget(ctx, admin, store, request.Reference)
	if err != nil {
		return PortSetPlan{}, err
	}
	summary := target.Summary
	if !tenantHasProject(summary, target.Project) {
		return PortSetPlan{}, fmt.Errorf("Sandcastle project %s not found in tenant %s", target.Project, summary.Tenant)
	}
	instanceName, err := naming.MachineIncusInstanceName(naming.MachineRef{Tenant: summary.Tenant, Project: target.Project, Machine: target.Name})
	if err != nil {
		return PortSetPlan{}, err
	}
	return PortSetPlan{
		Reference:    request.Reference,
		Tenant:       summary,
		Project:      target.Project,
		Name:         target.Name,
		InstanceName: instanceName,
		AppPort:      request.AppPort,
		CaddyFile:    caddy.RenderMachineHosts(MachineCaddyHostnames(target.Name, target.Project, summary.DNSSuffix), request.AppPort, MachineCertPath, MachineCertKeyPath),
	}, nil
}
