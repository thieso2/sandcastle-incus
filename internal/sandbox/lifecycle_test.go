package sandbox

import (
	"context"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

func TestPlanLifecycle(t *testing.T) {
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
	plan, err := PlanLifecycle(context.Background(), config.LoadAdminFromEnv(), project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-alice-myproject",
		Config: projectConfig,
	}}}, LifecycleRequest{Reference: "alice/myproject/codex", Action: ActionRestart})
	if err != nil {
		t.Fatal(err)
	}
	if plan.InstanceName != "sc-codex" {
		t.Fatalf("InstanceName = %q", plan.InstanceName)
	}
	if plan.Action != ActionRestart {
		t.Fatalf("Action = %q", plan.Action)
	}
}

func TestPlanLifecycleRejectsUnknownAction(t *testing.T) {
	_, err := PlanLifecycle(context.Background(), config.LoadAdminFromEnv(), project.MemoryStore{}, LifecycleRequest{
		Reference: "alice/myproject/codex",
		Action:    "bad",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
