package sandbox

import (
	"context"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

func TestPlanSetPort(t *testing.T) {
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
	plan, err := PlanSetPort(context.Background(), config.LoadAdminFromEnv(), project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-alice-myproject",
		Config: projectConfig,
	}}}, PortSetRequest{Reference: "alice/myproject/codex", AppPort: 5173})
	if err != nil {
		t.Fatal(err)
	}
	if plan.InstanceName != "sc-codex" {
		t.Fatalf("InstanceName = %q", plan.InstanceName)
	}
	if plan.AppPort != 5173 {
		t.Fatalf("AppPort = %d", plan.AppPort)
	}
	if !strings.Contains(plan.CaddyFile.Content, "reverse_proxy 127.0.0.1:5173") {
		t.Fatalf("CaddyFile.Content = %q", plan.CaddyFile.Content)
	}
}

func TestPlanSetPortRejectsInvalidPort(t *testing.T) {
	_, err := PlanSetPort(context.Background(), config.LoadAdminFromEnv(), project.MemoryStore{}, PortSetRequest{
		Reference: "alice/myproject/codex",
		AppPort:   70000,
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
