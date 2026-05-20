package sandbox

import (
	"context"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

func TestPlanCreate(t *testing.T) {
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
	plan, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-alice-myproject",
		Config: projectConfig,
	}}}, CreateRequest{Reference: "alice/myproject/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.InstanceName != "sc-codex" {
		t.Fatalf("InstanceName = %q", plan.InstanceName)
	}
	if plan.PrivateIP != "10.248.0.20" {
		t.Fatalf("PrivateIP = %q", plan.PrivateIP)
	}
	if plan.AppPort != DefaultAppPort {
		t.Fatalf("AppPort = %d", plan.AppPort)
	}
	if plan.MetadataConfig[meta.KeyKind] != meta.KindSandbox {
		t.Fatalf("metadata kind = %q", plan.MetadataConfig[meta.KeyKind])
	}
}

func TestPlanCreateRejectsReservedName(t *testing.T) {
	_, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), project.MemoryStore{}, CreateRequest{Reference: "alice/myproject/dns"})
	if err == nil {
		t.Fatal("expected error")
	}
}
