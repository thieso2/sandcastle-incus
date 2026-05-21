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
	if plan.RemoteName != "sandcastle-alice" {
		t.Fatalf("RemoteName = %q", plan.RemoteName)
	}
	if !plan.Restricted {
		t.Fatal("Restricted = false, want true")
	}
	if len(plan.Projects) != 0 {
		t.Fatalf("Projects = %#v, want empty", plan.Projects)
	}
}

func TestPlanDeleteUser(t *testing.T) {
	plan, err := PlanDeleteUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if plan.CertificateName != "sandcastle-alice" || !plan.Restricted {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanGrant(t *testing.T) {
	plan, err := PlanGrant(config.LoadAdminFromEnv(), GrantRequest{
		User:     "alice",
		Projects: []string{"acme"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Projects) != 1 || plan.Projects[0] != "sc-acme" {
		t.Fatalf("Projects = %#v", plan.Projects)
	}
}

func TestPlanGrantDeduplicatesProjects(t *testing.T) {
	plan, err := PlanGrant(config.LoadAdminFromEnv(), GrantRequest{
		User:     "alice",
		Projects: []string{"acme", "acme"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Projects) != 1 || plan.Projects[0] != "sc-acme" {
		t.Fatalf("Projects = %#v", plan.Projects)
	}
}

func TestPlanGrantRequiresProject(t *testing.T) {
	_, err := PlanGrant(config.LoadAdminFromEnv(), GrantRequest{User: "alice"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanGrantRejectsInvalidTenant(t *testing.T) {
	_, err := PlanGrant(config.LoadAdminFromEnv(), GrantRequest{
		User:     "alice",
		Projects: []string{"bob/project"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanTenantGrant(t *testing.T) {
	plan, err := PlanTenantGrant(config.LoadAdminFromEnv(), TenantAccessRequest{Tenant: "acme", User: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.User != "alice" || len(plan.Projects) != 1 || plan.Projects[0] != "sc-acme" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanTenantUsers(t *testing.T) {
	plan, err := PlanTenantUsers(config.LoadAdminFromEnv(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Tenant != "acme" || plan.IncusProject != "sc-acme" {
		t.Fatalf("plan = %#v", plan)
	}
}
