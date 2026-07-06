package usertrust

import (
	"slices"
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
	if !slices.Equal(plan.Projects, []string{"sc-acme", "sc-acme-infra", "sc-acme-native"}) {
		t.Fatalf("Projects = %#v", plan.Projects)
	}
}

func TestPlanGrantPersonalTenantAllowsGitHubUsernameNames(t *testing.T) {
	plan, err := PlanGrant(config.LoadAdminFromEnv(), GrantRequest{
		User:     "1octocat",
		Projects: []string{"1octocat"},
		Personal: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.CertificateName != "sandcastle-1octocat" || !slices.Equal(plan.Projects, []string{"sc-1octocat", "sc-1octocat-infra", "sc-1octocat-native"}) {
		t.Fatalf("plan = %#v", plan)
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
	if !slices.Equal(plan.Projects, []string{"sc-acme", "sc-acme-infra", "sc-acme-native"}) {
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
	if plan.User != "alice" || !slices.Equal(plan.Projects, []string{"sc-acme", "sc-acme-infra", "sc-acme-native"}) {
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

func TestRestrictedInstallName(t *testing.T) {
	if got := RestrictedInstallName("", "acme"); got != "sandcastle-acme" {
		t.Fatalf("got %q", got)
	}
	if got := RestrictedInstallName("sc", "acme"); got != "sandcastle-acme" {
		t.Fatalf("got %q", got)
	}
	if got := RestrictedInstallName("sc2", "acme"); got != "sandcastle-acme" {
		t.Fatalf("got %q", got)
	}
	// non-default installs qualify the name so two sandcastles on one host
	// (and one client) cannot collide on certificates or remotes
	if got := RestrictedInstallName("id", "acme"); got != "sandcastle-id-acme" {
		t.Fatalf("got %q", got)
	}
}
