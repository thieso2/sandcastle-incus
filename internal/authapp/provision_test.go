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
		TenantCreator: creator,
		Trust:         trust,
	}

	result, err := provisioner.EnsurePersonalTenant(context.Background(), User{UserKey: "1octocat"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Tenant != "1octocat" || result.IncusProject != "sc-1octocat" {
		t.Fatalf("result = %#v", result)
	}
	if len(creator.plans) != 1 || !tenantMetadataForProvisionTest(t, creator.plans[0]).Personal {
		t.Fatalf("created plans = %#v", creator.plans)
	}
	if result.Token != "token-1octocat" || result.RemoteName != "sandcastle-1octocat" || len(result.AccessibleTenants) != 1 || result.AccessibleTenants[0] != "1octocat" {
		t.Fatalf("result token fields = %#v", result)
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
			Config: tenantConfigForProvisionTest(t, meta.Tenant{Tenant: "1octocat", Personal: true, PrivateCIDR: "10.248.1.0/24"}),
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

type fakeTenantCreator struct {
	plans []tenant.CreatePlan
}

func (c *fakeTenantCreator) CreateTenant(ctx context.Context, plan tenant.CreatePlan) error {
	c.plans = append(c.plans, plan)
	return nil
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
