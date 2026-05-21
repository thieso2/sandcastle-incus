package hostoverride

import (
	"context"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestPlanAdd(t *testing.T) {
	plan, err := PlanAdd(context.Background(), config.LoadAdminFromEnv(), tenantStoreForTest(t), machineStoreForTest{}, AddRequest{
		Reference: "acme/default/codex",
		Hostname:  "Example.COM.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Hostname != "example.com" {
		t.Fatalf("Hostname = %q", plan.Hostname)
	}
	if plan.IPAddress != "10.248.0.20" {
		t.Fatalf("IPAddress = %q", plan.IPAddress)
	}
	if len(plan.ExtraSANs) != 1 || plan.ExtraSANs[0] != "example.com" {
		t.Fatalf("ExtraSANs = %#v", plan.ExtraSANs)
	}
	if plan.Reference != "acme/default/codex" {
		t.Fatalf("Reference = %q", plan.Reference)
	}
	if !strings.Contains(plan.HostsEntry.Line, "10.248.0.20 example.com") {
		t.Fatalf("HostsEntry = %#v", plan.HostsEntry)
	}
	if !plan.RequiresReissue || !plan.RequiresHostsEdit {
		t.Fatalf("plan requirements = %#v", plan)
	}
}

func TestPlanAddSupportsCurrentTenantAndProject(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	admin.Project = "website"
	plan, err := PlanAdd(context.Background(), admin, tenantStoreForTest(t), machineStoreWithMachines{machines: []meta.Machine{{
		Tenant:    "acme",
		Project:   "website",
		Name:      "codex",
		PrivateIP: "10.248.0.20",
	}}}, AddRequest{
		Reference: "codex",
		Hostname:  "example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Tenant.Tenant != "acme" || plan.Machine.Project != "website" || plan.Machine.Name != "codex" {
		t.Fatalf("plan = %#v", plan)
	}
	if plan.Reference != "acme/website/codex" {
		t.Fatalf("Reference = %q", plan.Reference)
	}
	if !strings.Contains(plan.HostsEntry.BeginLine, "acme/website/codex example.com") {
		t.Fatalf("HostsEntry = %#v", plan.HostsEntry)
	}
}

func TestPlanDeleteUsesCanonicalHostsEntry(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	admin.Project = "website"
	plan, err := PlanDelete(context.Background(), admin, tenantStoreForTest(t), machineStoreForTest{}, DeleteRequest{
		Reference: "codex",
		Hostname:  "example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Reference != "acme/website/codex" {
		t.Fatalf("Reference = %q", plan.Reference)
	}
	if !strings.Contains(plan.HostsEntry.BeginLine, "acme/website/codex example.com") {
		t.Fatalf("HostsEntry = %#v", plan.HostsEntry)
	}
}

func TestPlanAddRejectsHostnameAssignedToAnotherMachine(t *testing.T) {
	_, err := PlanAdd(context.Background(), config.LoadAdminFromEnv(), tenantStoreForTest(t), machineStoreWithMachines{
		machines: []meta.Machine{
			{Tenant: "acme", Project: "default", Name: "codex", PrivateIP: "10.248.0.20"},
			{Tenant: "acme", Project: "default", Name: "web", PrivateIP: "10.248.0.21", ExtraSANs: []string{"example.com"}},
		},
	}, AddRequest{
		Reference: "acme/default/codex",
		Hostname:  "example.com",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "already assigned") {
		t.Fatalf("error = %q", err)
	}
}

func TestPlanAddRejectsWildcardHostname(t *testing.T) {
	_, err := PlanAdd(context.Background(), config.LoadAdminFromEnv(), tenantStoreForTest(t), machineStoreForTest{}, AddRequest{
		Reference: "acme/default/codex",
		Hostname:  "*.example.com",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanAddRejectsIPAddress(t *testing.T) {
	_, err := PlanAdd(context.Background(), config.LoadAdminFromEnv(), tenantStoreForTest(t), machineStoreForTest{}, AddRequest{
		Reference: "acme/default/codex",
		Hostname:  "192.0.2.1",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanDelete(t *testing.T) {
	plan, err := PlanDelete(context.Background(), config.LoadAdminFromEnv(), tenantStoreForTest(t), machineStoreForTest{}, DeleteRequest{
		Reference: "acme/default/codex",
		Hostname:  "example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Hostname != "example.com" {
		t.Fatalf("Hostname = %q", plan.Hostname)
	}
	if plan.HostsEntry.Line != "10.248.0.20 example.com" {
		t.Fatalf("HostsEntry = %#v", plan.HostsEntry)
	}
}

func TestPlanList(t *testing.T) {
	result, err := PlanList(context.Background(), config.LoadAdminFromEnv(), tenantStoreForTest(t), machineStoreForTest{}, ListRequest{Reference: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Overrides) != 1 {
		t.Fatalf("len(Overrides) = %d", len(result.Overrides))
	}
	if result.Overrides[0].Hostname != "example.com" {
		t.Fatalf("Hostname = %q", result.Overrides[0].Hostname)
	}
}

func TestPlanListSupportsCurrentTenant(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	result, err := PlanList(context.Background(), admin, tenantStoreForTest(t), machineStoreForTest{}, ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Tenant.Tenant != "acme" {
		t.Fatalf("tenant = %#v", result.Tenant)
	}
}

type machineStoreForTest struct{}

func (s machineStoreForTest) FindMachine(ctx context.Context, summary tenant.Summary, projectName string, machineName string) (meta.Machine, error) {
	return meta.Machine{
		Tenant:    summary.Tenant,
		Project:   projectName,
		Name:      machineName,
		AppPort:   3000,
		PrivateIP: "10.248.0.20",
		ExtraSANs: []string{"example.com"},
	}, nil
}

func (s machineStoreForTest) ListMachines(ctx context.Context, summary tenant.Summary) ([]meta.Machine, error) {
	machine, err := s.FindMachine(ctx, summary, "default", "codex")
	if err != nil {
		return nil, err
	}
	return []meta.Machine{machine}, nil
}

type machineStoreWithMachines struct {
	machines []meta.Machine
}

func (s machineStoreWithMachines) FindMachine(ctx context.Context, summary tenant.Summary, projectName string, machineName string) (meta.Machine, error) {
	for _, machine := range s.machines {
		if machine.Project == projectName && machine.Name == machineName {
			return machine, nil
		}
	}
	return meta.Machine{}, nil
}

func (s machineStoreWithMachines) ListMachines(ctx context.Context, summary tenant.Summary) ([]meta.Machine, error) {
	return s.machines, nil
}

func tenantStoreForTest(t *testing.T) tenant.MemoryStore {
	t.Helper()
	tenantConfig, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	return tenant.MemoryStore{Projects: []tenant.IncusProject{{
		Name:   "sc-acme",
		Config: tenantConfig,
	}}}
}
