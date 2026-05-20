package project

import (
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
)

func TestPlanDelete(t *testing.T) {
	plan, err := PlanDelete(config.LoadAdminFromEnv(), DeleteRequest{
		Reference: "alice/myproject",
		Purge:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.IncusProject != "sc-alice-myproject" {
		t.Fatalf("IncusProject = %q", plan.IncusProject)
	}
	if !plan.PurgeDurableState {
		t.Fatal("PurgeDurableState = false, want true")
	}
	if len(plan.DurableVolumes) != 3 {
		t.Fatalf("durable volumes = %d, want 3", len(plan.DurableVolumes))
	}
}
