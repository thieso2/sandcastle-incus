package machine

import (
	"context"
	"fmt"
	"io"

	"github.com/thieso2/sandcastle-incus/internal/config"
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
	UserID       int             `json:"userId"`
	GroupID      int             `json:"groupId"`
	WorkingDir   string          `json:"workingDir"`
	Interactive  bool            `json:"interactive"`
	Managed      bool            `json:"managed"`
}

type EnterSession struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type Enterer interface {
	ConnectMachine(context.Context, EnterPlan, EnterSession) error
}

func PlanEnter(ctx context.Context, admin config.Admin, store project.IncusProjectStore, machineStore Store, request EnterRequest) (EnterPlan, error) {
	if err := admin.Validate(); err != nil {
		return EnterPlan{}, err
	}
	resolved, err := resolveExistingMachine(ctx, admin, store, machineStore, request.Reference)
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
	linuxUser := resolved.Summary.Tenant
	workingDir := "/workspace"
	userID := DefaultLinuxUID
	groupID := DefaultLinuxGID
	if !resolved.Managed {
		linuxUser = "root"
		workingDir = "/root"
		userID = 0
		groupID = 0
	}
	return EnterPlan{
		Reference:    request.Reference,
		Tenant:       resolved.Summary,
		Project:      resolved.Project,
		Name:         resolved.Name,
		InstanceName: resolved.InstanceName,
		Command:      command,
		LinuxUser:    linuxUser,
		UserID:       userID,
		GroupID:      groupID,
		WorkingDir:   workingDir,
		Interactive:  interactive,
		Managed:      resolved.Managed,
	}, nil
}
