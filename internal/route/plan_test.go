package route

import (
	"context"
	"testing"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type fakeSandboxStore struct {
	sandbox meta.Sandbox
}

func (s fakeSandboxStore) FindSandbox(ctx context.Context, summary project.Summary, name string) (meta.Sandbox, error) {
	return s.sandbox, nil
}

func TestPlanAddPinsCurrentSandboxAppPort(t *testing.T) {
	plan, err := PlanAdd(context.Background(), scconfig.LoadAdminFromEnv(), projectStoreForTest(t), fakeSandboxStore{sandbox: meta.Sandbox{
		Owner:     "alice",
		Project:   "myproject",
		Name:      "codex",
		AppPort:   5173,
		PrivateIP: "10.248.0.20",
	}}, AddRequest{Hostname: "App.Example.COM", TargetReference: "alice/myproject/codex"})
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

func TestPlanAddFallsBackToDefaultAppPort(t *testing.T) {
	plan, err := PlanAdd(context.Background(), scconfig.LoadAdminFromEnv(), projectStoreForTest(t), fakeSandboxStore{sandbox: meta.Sandbox{
		Owner:     "alice",
		Project:   "myproject",
		Name:      "codex",
		PrivateIP: "10.248.0.20",
	}}, AddRequest{Hostname: "app.example.com", TargetReference: "alice/myproject/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.RoutePort != 3000 {
		t.Fatalf("RoutePort = %d", plan.RoutePort)
	}
}

func TestPlanAddRejectsWildcardRoute(t *testing.T) {
	_, err := PlanAdd(context.Background(), scconfig.LoadAdminFromEnv(), projectStoreForTest(t), fakeSandboxStore{}, AddRequest{
		Hostname:        "*.example.com",
		TargetReference: "alice/myproject/codex",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanRemove(t *testing.T) {
	plan, err := PlanRemove(scconfig.LoadAdminFromEnv(), RemoveRequest{Hostname: "App.Example.COM."})
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

func projectStoreForTest(t *testing.T) project.MemoryStore {
	t.Helper()
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	return project.MemoryStore{Projects: []project.IncusProject{{Name: "sc-alice-myproject", Config: configMap}}}
}
