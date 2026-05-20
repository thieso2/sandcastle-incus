package usertrust

import (
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
)

func TestPlanCreateUser(t *testing.T) {
	plan, err := PlanCreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if plan.CertificateName != "sandcastle-alice" {
		t.Fatalf("CertificateName = %q", plan.CertificateName)
	}
	if !plan.Restricted {
		t.Fatal("Restricted = false, want true")
	}
	if len(plan.Projects) != 0 {
		t.Fatalf("Projects = %#v, want empty", plan.Projects)
	}
}

func TestPlanGrant(t *testing.T) {
	plan, err := PlanGrant(config.LoadAdminFromEnv(), GrantRequest{
		User:     "alice",
		Projects: []string{"alice/myproject"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Projects) != 1 || plan.Projects[0] != "sc-alice-myproject" {
		t.Fatalf("Projects = %#v", plan.Projects)
	}
}

func TestPlanGrantDeduplicatesProjects(t *testing.T) {
	plan, err := PlanGrant(config.LoadAdminFromEnv(), GrantRequest{
		User:     "alice",
		Projects: []string{"alice/myproject", "alice/myproject"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Projects) != 1 || plan.Projects[0] != "sc-alice-myproject" {
		t.Fatalf("Projects = %#v", plan.Projects)
	}
}

func TestPlanGrantRequiresProject(t *testing.T) {
	_, err := PlanGrant(config.LoadAdminFromEnv(), GrantRequest{User: "alice"})
	if err == nil {
		t.Fatal("expected error")
	}
}
