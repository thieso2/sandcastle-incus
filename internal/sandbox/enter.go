package sandbox

import (
	"context"
	"fmt"
	"io"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type EnterRequest struct {
	Reference string
	Command   []string
}

type EnterPlan struct {
	Reference    string          `json:"reference"`
	Project      project.Summary `json:"project"`
	Name         string          `json:"name"`
	InstanceName string          `json:"instanceName"`
	Command      []string        `json:"command"`
	WorkingDir   string          `json:"workingDir"`
	Interactive  bool            `json:"interactive"`
}

type EnterSession struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type Enterer interface {
	EnterSandbox(context.Context, EnterPlan, EnterSession) error
}

func PlanEnter(ctx context.Context, admin config.Admin, store project.IncusProjectStore, request EnterRequest) (EnterPlan, error) {
	if err := admin.Validate(); err != nil {
		return EnterPlan{}, err
	}
	projectRef, sandboxName, err := parseSandboxRef(request.Reference)
	if err != nil {
		return EnterPlan{}, err
	}
	summary, err := findProject(ctx, store, projectRef)
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
		Project:      summary,
		Name:         sandboxName,
		InstanceName: "sc-" + sandboxName,
		Command:      command,
		WorkingDir:   "/workspace",
		Interactive:  interactive,
	}, nil
}
