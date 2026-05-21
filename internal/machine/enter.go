package machine

import (
	"context"
	"fmt"
	"io"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type EnterRequest struct {
	Reference string
	Command   []string
}

type EnterPlan struct {
	Reference    string          `json:"reference"`
	Tenant       project.Summary `json:"tenant"`
	Project      string          `json:"project"`
	Name         string          `json:"name"`
	InstanceName string          `json:"instanceName"`
	Command      []string        `json:"command"`
	LinuxUser    string          `json:"linuxUser"`
	WorkingDir   string          `json:"workingDir"`
	Interactive  bool            `json:"interactive"`
}

type EnterSession struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type Enterer interface {
	ConnectMachine(context.Context, EnterPlan, EnterSession) error
}

func PlanEnter(ctx context.Context, admin config.Admin, store project.IncusProjectStore, request EnterRequest) (EnterPlan, error) {
	if err := admin.Validate(); err != nil {
		return EnterPlan{}, err
	}
	tenantRef, err := currentTenantRef(admin)
	if err != nil {
		return EnterPlan{}, err
	}
	projectRef, machineName, err := naming.ParseUserMachineRef(request.Reference, admin.Project)
	if err != nil {
		return EnterPlan{}, err
	}
	summary, err := findTenant(ctx, store, tenantRef)
	if err != nil {
		return EnterPlan{}, err
	}
	if !tenantHasProject(summary, projectRef.Project) {
		return EnterPlan{}, fmt.Errorf("Sandcastle project %s not found in tenant %s", projectRef.Project, summary.Tenant)
	}
	instanceName, err := naming.MachineIncusInstanceName(naming.MachineRef{Tenant: summary.Tenant, Project: projectRef.Project, Machine: machineName})
	if err != nil {
		return EnterPlan{}, err
	}
	command := request.Command
	interactive := false
	if len(command) == 0 {
		command = []string{"/bin/bash", "-l"}
		interactive = true
	}
	if len(command) == 0 || command[0] == "" {
		return EnterPlan{}, fmt.Errorf("enter command is required")
	}
	return EnterPlan{
		Reference:    request.Reference,
		Tenant:       summary,
		Project:      projectRef.Project,
		Name:         machineName,
		InstanceName: instanceName,
		Command:      command,
		LinuxUser:    summary.Tenant,
		WorkingDir:   "/workspace",
		Interactive:  interactive,
	}, nil
}
