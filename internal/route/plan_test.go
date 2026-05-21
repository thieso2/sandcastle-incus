package route

import (
	"context"
	"testing"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type fakeMachineStore struct {
	machine meta.Machine
}

func (s fakeMachineStore) FindMachine(ctx context.Context, summary tenant.Summary, projectName string, machineName string) (meta.Machine, error) {
	return s.machine, nil
}

func TestPlanCreatePinsCurrentMachineAppPort(t *testing.T) {
	plan, err := PlanCreate(context.Background(), routeAdminForTest(), tenantStoreForTest(t), fakeMachineStore{machine: meta.Machine{
		Tenant:    "acme",
		Project:   "default",
		Name:      "codex",
		AppPort:   5173,
		PrivateIP: "10.248.0.20",
	}}, CreateRequest{Hostname: "App.Example.COM", TargetReference: "acme/default/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Hostname != "app.example.com" {
		t.Fatalf("Hostname = %q", plan.Hostname)
	}
	if plan.RoutePort != 5173 {
		t.Fatalf("RoutePort = %d", plan.RoutePort)
	}
	if plan.TargetIP != "10.248.0.20" {
		t.Fatalf("TargetIP = %q", plan.TargetIP)
	}
	if plan.TargetInstanceName != "default-codex" {
		t.Fatalf("TargetInstanceName = %q", plan.TargetInstanceName)
	}
	if plan.IngressDevice != "sc-route-app-example-com" {
		t.Fatalf("IngressDevice = %q", plan.IngressDevice)
	}
	if !plan.DNSProof.Required || plan.DNSProof.Hostname != "app.example.com" {
		t.Fatalf("DNSProof = %#v", plan.DNSProof)
	}
	if plan.DNSProof.ExpectedTarget != "203.0.113.10" {
		t.Fatalf("DNSProof.ExpectedTarget = %q", plan.DNSProof.ExpectedTarget)
	}
	routeMetadata, err := meta.ParseRouteConfig(plan.MetadataConfig)
	if err != nil {
		t.Fatal(err)
	}
	if routeMetadata.Hostname != "app.example.com" || routeMetadata.RoutePort != 5173 {
		t.Fatalf("route metadata = %#v", routeMetadata)
	}
	if !plan.RequiresBroker {
		t.Fatal("expected broker requirement")
	}
}

func TestPlanCreateSupportsCurrentTenantAndProject(t *testing.T) {
	admin := routeAdminForTest()
	admin.Tenant = "acme"
	admin.Project = "website"
	plan, err := PlanCreate(context.Background(), admin, tenantStoreForTest(t), fakeMachineStore{machine: meta.Machine{
		Tenant:    "acme",
		Project:   "website",
		Name:      "codex",
		AppPort:   5173,
		PrivateIP: "10.248.0.20",
	}}, CreateRequest{Hostname: "app.example.com", TargetReference: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetReference != "acme/website/codex" {
		t.Fatalf("TargetReference = %q", plan.TargetReference)
	}
}

func TestPlanCreateFallsBackToDefaultAppPort(t *testing.T) {
	plan, err := PlanCreate(context.Background(), routeAdminForTest(), tenantStoreForTemplate(t, "ai"), fakeMachineStore{machine: meta.Machine{
		Tenant:    "acme",
		Project:   "default",
		Name:      "codex",
		Template:  "base",
		PrivateIP: "10.248.0.20",
	}}, CreateRequest{Hostname: "app.example.com", TargetReference: "acme/default/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.RoutePort != 3000 {
		t.Fatalf("RoutePort = %d", plan.RoutePort)
	}
}

func TestPlanCreateRejectsUnknownTemplateAppPortFallback(t *testing.T) {
	_, err := PlanCreate(context.Background(), routeAdminForTest(), tenantStoreForTemplate(t, "ai"), fakeMachineStore{machine: meta.Machine{
		Tenant:    "acme",
		Project:   "default",
		Name:      "codex",
		Template:  "unknown",
		PrivateIP: "10.248.0.20",
	}}, CreateRequest{Hostname: "app.example.com", TargetReference: "acme/default/codex"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanCreateRejectsWildcardRoute(t *testing.T) {
	_, err := PlanCreate(context.Background(), routeAdminForTest(), tenantStoreForTest(t), fakeMachineStore{}, CreateRequest{
		Hostname:        "*.example.com",
		TargetReference: "acme/default/codex",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanCreateRequiresInfrastructureHost(t *testing.T) {
	_, err := PlanCreate(context.Background(), scconfig.LoadAdminFromEnv(), tenantStoreForTest(t), fakeMachineStore{}, CreateRequest{
		Hostname:        "app.example.com",
		TargetReference: "acme/default/codex",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanDelete(t *testing.T) {
	plan, err := PlanDelete(scconfig.LoadAdminFromEnv(), DeleteRequest{Hostname: "App.Example.COM."})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Hostname != "app.example.com" {
		t.Fatalf("Hostname = %q", plan.Hostname)
	}
	if plan.IncusProjectPrefix != "sc" {
		t.Fatalf("IncusProjectPrefix = %q", plan.IncusProjectPrefix)
	}
	if !plan.RequiresBroker {
		t.Fatal("expected broker requirement")
	}
}

func TestPlanStatus(t *testing.T) {
	plan, err := PlanStatus(scconfig.LoadAdminFromEnv(), StatusRequest{Hostname: "App.Example.COM."})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Hostname != "app.example.com" {
		t.Fatalf("Hostname = %q", plan.Hostname)
	}
	if !plan.RequiresBroker {
		t.Fatal("expected broker requirement")
	}
}

func TestProfileName(t *testing.T) {
	if got := ProfileName("App.Example.COM"); got != "sc-route-app-example-com" {
		t.Fatalf("ProfileName = %q", got)
	}
}

func tenantStoreForTest(t *testing.T) tenant.MemoryStore {
	t.Helper()
	return tenantStoreForTemplate(t, "ai")
}

func routeAdminForTest() scconfig.Admin {
	admin := scconfig.LoadAdminFromEnv()
	admin.InfrastructureHost = "203.0.113.10"
	return admin
}

func tenantStoreForTemplate(t *testing.T, template string) tenant.MemoryStore {
	t.Helper()
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	return tenant.MemoryStore{Projects: []tenant.IncusProject{{Name: "sc-acme", Config: configMap}}}
}
