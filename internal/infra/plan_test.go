package infra

import (
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

func TestPlanCreate(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	plan, err := PlanCreate(admin, CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project != config.DefaultInfrastructureProject {
		t.Fatalf("Project = %q", plan.Project)
	}
	if plan.CaddyInstance != route.InfrastructureCaddyName {
		t.Fatalf("CaddyInstance = %q", plan.CaddyInstance)
	}
	if plan.RouteBrokerInstance != RouteBrokerName {
		t.Fatalf("RouteBrokerInstance = %q", plan.RouteBrokerInstance)
	}
	if len(plan.Instances) != 2 {
		t.Fatalf("instances = %d, want 2", len(plan.Instances))
	}
	if plan.Instances[0].Name != route.InfrastructureCaddyName || plan.Instances[0].Role != "caddy" {
		t.Fatalf("caddy instance = %#v", plan.Instances[0])
	}
	if plan.Instances[1].Name != RouteBrokerName || plan.Instances[1].Role != "route-broker" {
		t.Fatalf("route broker instance = %#v", plan.Instances[1])
	}
	if plan.Instances[0].Devices["root"]["pool"] != admin.StoragePool {
		t.Fatalf("root device = %#v", plan.Instances[0].Devices["root"])
	}
}
