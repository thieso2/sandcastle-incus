package authapp

import (
	"context"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

func TestProvisionerCreatesPersonalTenantAndReturnsRestrictedToken(t *testing.T) {
	creator := &fakeTenantCreator{}
	trust := &fakeTokenCreator{}
	provisioner := Provisioner{
		Admin: config.LoadAdminFromEnv(),
		Tenants: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-existing",
			Config: tenantConfigForProvisionTest(t, meta.Tenant{Tenant: "existing", PrivateCIDR: "10.248.0.0/24"}),
		}}},
		TenantCreator:   creator,
		Trust:           trust,
		DefaultUnixUser: "localuser",
	}

	result, err := provisioner.EnsurePersonalTenant(context.Background(), User{UserKey: "1octocat"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Tenant != "1octocat" || result.IncusProject != "sc-1octocat" {
		t.Fatalf("result = %#v", result)
	}
	metadata := tenantMetadataForProvisionTest(t, creator.plans[0])
	if len(creator.plans) != 1 || !metadata.Personal {
		t.Fatalf("created plans = %#v", creator.plans)
	}
	if metadata.UnixUser != "localuser" {
		t.Fatalf("UnixUser = %q", metadata.UnixUser)
	}
	if result.Token != "token-1octocat" || result.RemoteName != "sandcastle-1octocat" || len(result.AccessibleTenants) != 1 || result.AccessibleTenants[0] != "1octocat" {
		t.Fatalf("result token fields = %#v", result)
	}
	if result.CurrentProject != "default" || !result.DefaultProjectReady || !result.TenantTailnetReady {
		t.Fatalf("result readiness fields = %#v", result)
	}
	if len(trust.plans) != 1 || trust.plans[0].User != "1octocat" || trust.plans[0].Projects[0] != "sc-1octocat" {
		t.Fatalf("trust plans = %#v", trust.plans)
	}
}

func TestProvisionerSkipsTenantCreateWhenPersonalTenantExists(t *testing.T) {
	creator := &fakeTenantCreator{}
	trust := &fakeTokenCreator{}
	provisioner := Provisioner{
		Admin: config.LoadAdminFromEnv(),
		Tenants: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-1octocat",
			Config: tenantConfigForProvisionTest(t, meta.Tenant{Tenant: "1octocat", Personal: true, PrivateCIDR: "10.248.1.0/24", Projects: []meta.Project{{Name: "default"}}}),
		}}},
		TenantCreator: creator,
		Trust:         trust,
	}

	if _, err := provisioner.EnsurePersonalTenant(context.Background(), User{UserKey: "1octocat"}); err != nil {
		t.Fatal(err)
	}
	if len(creator.plans) != 0 {
		t.Fatalf("created plans = %#v", creator.plans)
	}
	if len(trust.plans) != 1 {
		t.Fatalf("trust plans = %#v", trust.plans)
	}
}

func TestProvisionerRepairsMissingDefaultProjectOnExistingPersonalTenant(t *testing.T) {
	creator := &fakeTenantCreator{}
	trust := &fakeTokenCreator{}
	updater := &fakeProjectUpdater{}
	provisioner := Provisioner{
		Admin: config.LoadAdminFromEnv(),
		Tenants: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-1octocat",
			Config: tenantConfigForProvisionTest(t, meta.Tenant{Tenant: "1octocat", Personal: true, PrivateCIDR: "10.248.1.0/24", Projects: []meta.Project{{Name: "work"}}}),
		}}},
		TenantCreator:  creator,
		ProjectUpdater: updater,
		Trust:          trust,
	}

	result, err := provisioner.EnsurePersonalTenant(context.Background(), User{UserKey: "1octocat"})
	if err != nil {
		t.Fatal(err)
	}
	if len(creator.plans) != 0 {
		t.Fatalf("created plans = %#v", creator.plans)
	}
	if len(updater.calls) != 1 || updater.calls[0].incusProject != "sc-1octocat" || !projectListHas(updater.calls[0].projects, "default") || !projectListHas(updater.calls[0].projects, "work") {
		t.Fatalf("updater calls = %#v", updater.calls)
	}
	if !result.DefaultProjectReady || result.CurrentProject != "default" {
		t.Fatalf("result = %#v", result)
	}
}

type fakeTenantCreator struct {
	plans []tenant.CreatePlan
}

func (c *fakeTenantCreator) CreateTenant(ctx context.Context, plan tenant.CreatePlan) error {
	c.plans = append(c.plans, plan)
	return nil
}

type fakeProjectUpdater struct {
	calls []struct {
		incusProject string
		projects     []meta.Project
	}
}

func (u *fakeProjectUpdater) SetTenantProjects(ctx context.Context, incusProjectName string, projects []meta.Project) error {
	u.calls = append(u.calls, struct {
		incusProject string
		projects     []meta.Project
	}{incusProject: incusProjectName, projects: append([]meta.Project{}, projects...)})
	return nil
}

func projectListHas(projects []meta.Project, name string) bool {
	for _, project := range projects {
		if project.Name == name {
			return true
		}
	}
	return false
}

type fakeTokenCreator struct {
	plans []usertrust.UserPlan
}

func (g *fakeTokenCreator) CreateToken(ctx context.Context, plan usertrust.UserPlan) (usertrust.TokenResult, error) {
	g.plans = append(g.plans, plan)
	return usertrust.TokenResult{
		User:            plan.User,
		CertificateName: plan.CertificateName,
		RemoteName:      plan.RemoteName,
		Restricted:      plan.Restricted,
		Projects:        append([]string{}, plan.Projects...),
		Token:           "token-" + plan.User,
	}, nil
}

func tenantConfigForProvisionTest(t *testing.T, value meta.Tenant) map[string]string {
	t.Helper()
	config, err := meta.TenantConfig(value)
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func tenantMetadataForProvisionTest(t *testing.T, plan tenant.CreatePlan) meta.Tenant {
	t.Helper()
	metadata, err := meta.ParseTenantConfig(plan.TenantMetadataConfig)
	if err != nil {
		t.Fatal(err)
	}
	return metadata
}
