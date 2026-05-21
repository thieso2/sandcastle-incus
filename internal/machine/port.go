package machine

import (
	"context"
	"fmt"

	"github.com/thieso2/sandcastle-incus/internal/caddy"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type PortSetRequest struct {
	Reference string
	AppPort   int
}

type PortSetPlan struct {
	Reference    string          `json:"reference"`
	Tenant       project.Summary `json:"tenant"`
	Project      string          `json:"project"`
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
	tenantRef, err := currentTenantRef(admin)
	if err != nil {
		return PortSetPlan{}, err
	}
	projectRef, machineName, err := naming.ParseUserMachineRef(request.Reference, admin.Project)
	if err != nil {
		return PortSetPlan{}, err
	}
	summary, err := findTenant(ctx, store, tenantRef)
	if err != nil {
		return PortSetPlan{}, err
	}
	if !tenantHasProject(summary, projectRef.Project) {
		return PortSetPlan{}, fmt.Errorf("Sandcastle project %s not found in tenant %s", projectRef.Project, summary.Tenant)
	}
	instanceName, err := naming.MachineIncusInstanceName(naming.MachineRef{Tenant: summary.Tenant, Project: projectRef.Project, Machine: machineName})
	if err != nil {
		return PortSetPlan{}, err
	}
	return PortSetPlan{
		Reference:    request.Reference,
		Tenant:       summary,
		Project:      projectRef.Project,
		Name:         machineName,
		InstanceName: instanceName,
		AppPort:      request.AppPort,
		CaddyFile:    caddy.RenderSandbox(machineName+"."+projectRef.Project+"."+summary.DNSSuffix, request.AppPort, MachineCertPath, MachineCertKeyPath),
	}, nil
}
