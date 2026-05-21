package machine

import (
	"context"
	"fmt"
	"io"

	"github.com/thieso2/sandcastle-incus/internal/config"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type ConnectRequest struct {
	Reference string
	Command   []string
}

type ConnectPlan struct {
	Reference    string         `json:"reference"`
	Tenant       tenant.Summary `json:"tenant"`
	Project      string         `json:"project"`
	Name         string         `json:"name"`
	InstanceName string         `json:"instanceName"`
	Command      []string       `json:"command"`
	LinuxUser    string         `json:"linuxUser"`
	WorkingDir   string         `json:"workingDir"`
	Interactive  bool           `json:"interactive"`
}

type ConnectSession struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type Connector interface {
	ConnectMachine(context.Context, ConnectPlan, ConnectSession) error
}

func PlanConnect(ctx context.Context, admin config.Admin, store tenant.IncusTenantStore, machineStore Store, request ConnectRequest) (ConnectPlan, error) {
	if err := admin.Validate(); err != nil {
		return ConnectPlan{}, err
	}
	resolved, err := resolveExistingMachine(ctx, admin, store, machineStore, request.Reference)
	if err != nil {
		return ConnectPlan{}, err
	}
	command := request.Command
	interactive := false
	if len(command) == 0 {
		command = []string{"/bin/bash", "-l"}
		interactive = true
	}
	if len(command) == 0 || command[0] == "" {
		return ConnectPlan{}, fmt.Errorf("connect command is required")
	}
	return ConnectPlan{
		Reference:    request.Reference,
		Tenant:       resolved.Summary,
		Project:      resolved.Project,
		Name:         resolved.Name,
		InstanceName: resolved.InstanceName,
		Command:      command,
		LinuxUser:    resolved.Summary.Tenant,
		WorkingDir:   "/workspace",
		Interactive:  interactive,
	}, nil
}
