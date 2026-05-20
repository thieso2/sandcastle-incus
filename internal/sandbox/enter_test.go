package sandbox

import (
	"context"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

func TestPlanEnter(t *testing.T) {
	projectConfig, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanEnter(context.Background(), config.LoadAdminFromEnv(), project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-alice-myproject",
		Config: projectConfig,
	}}}, EnterRequest{Reference: "alice/myproject/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.InstanceName != "sc-codex" {
		t.Fatalf("InstanceName = %q", plan.InstanceName)
	}
	if len(plan.Command) != 2 || plan.Command[0] != "/bin/bash" || plan.Command[1] != "-l" {
		t.Fatalf("Command = %#v", plan.Command)
	}
	if plan.WorkingDir != "/workspace" {
		t.Fatalf("WorkingDir = %q", plan.WorkingDir)
	}
}
