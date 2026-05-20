package images

import (
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
)

func TestPlanSyncBaseImage(t *testing.T) {
	plan, err := PlanSync(config.LoadAdminFromEnv(), SyncRequest{SourceRef: "sandcastle/base:debian-13"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Template != "base" {
		t.Fatalf("Template = %q", plan.Template)
	}
	if plan.Alias != config.DefaultBaseImageAlias {
		t.Fatalf("Alias = %q", plan.Alias)
	}
	if !strings.Contains(plan.Description, "debian-13") {
		t.Fatalf("Description = %q", plan.Description)
	}
}

func TestPlanSyncAIImage(t *testing.T) {
	plan, err := PlanSync(config.LoadAdminFromEnv(), SyncRequest{SourceRef: "sandcastle/ai:debian-13"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Template != "ai" {
		t.Fatalf("Template = %q", plan.Template)
	}
	if plan.Alias != config.DefaultAIImageAlias {
		t.Fatalf("Alias = %q", plan.Alias)
	}
}

func TestPlanSyncRejectsUnknownImage(t *testing.T) {
	_, err := PlanSync(config.LoadAdminFromEnv(), SyncRequest{SourceRef: "other/image:latest"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "base or AI") {
		t.Fatalf("error = %q", err)
	}
}
