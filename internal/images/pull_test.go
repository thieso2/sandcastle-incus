package images

import (
	"strings"
	"testing"
)

func TestPlanPullBothTemplates(t *testing.T) {
	plan, err := PlanPull(testAdmin(), PullRequest{TenantProject: "sc-thieso2"})
	if err != nil {
		t.Fatalf("PlanPull: %v", err)
	}
	if len(plan.Commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(plan.Commands))
	}
	want := "incus image copy big:sandcastle/base:latest big: --project default --target-project sc-thieso2 --copy-aliases --reuse"
	if got := strings.Join(plan.Commands[0], " "); got != want {
		t.Errorf("base command =\n  %s\nwant\n  %s", got, want)
	}
	if plan.Aliases[1] != "sandcastle/ai:latest" {
		t.Errorf("ai alias = %q", plan.Aliases[1])
	}
}

func TestPlanPullSingleTemplate(t *testing.T) {
	plan, err := PlanPull(testAdmin(), PullRequest{TenantProject: "sc-thieso2", Templates: []string{"ai"}})
	if err != nil {
		t.Fatalf("PlanPull: %v", err)
	}
	if len(plan.Commands) != 1 || plan.Aliases[0] != "sandcastle/ai:latest" {
		t.Errorf("expected only ai, got %v", plan.Aliases)
	}
}

func TestPlanPullRequiresTenantProject(t *testing.T) {
	if _, err := PlanPull(testAdmin(), PullRequest{}); err == nil {
		t.Fatal("expected error when tenant project is missing")
	}
}
