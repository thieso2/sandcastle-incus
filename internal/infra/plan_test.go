package infra

import (
	"strings"
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
	if len(plan.RuntimeDirectories) == 0 {
		t.Fatal("expected runtime directories")
	}
	if runtimeFileContent(plan, route.InfrastructureCaddyName, "/etc/caddy/Caddyfile") == "" {
		t.Fatal("expected bootstrap infrastructure Caddyfile")
	}
	env := runtimeFileContent(plan, RouteBrokerName, RouteBrokerEnvPath)
	if !strings.Contains(env, "SANDCASTLE_ROUTE_BROKER_LISTEN=:9443") {
		t.Fatalf("env = %q", env)
	}
	if !strings.Contains(runtimeFileContent(plan, RouteBrokerName, RouteBrokerCertPath), "CERTIFICATE") {
		t.Fatal("expected route broker certificate PEM")
	}
	if !strings.Contains(runtimeFileContent(plan, RouteBrokerName, RouteBrokerKeyPath), "PRIVATE KEY") {
		t.Fatal("expected route broker private key PEM")
	}
	service := runtimeFileContent(plan, RouteBrokerName, RouteBrokerServicePath)
	if !strings.Contains(service, "admin route-broker serve") {
		t.Fatalf("service = %q", service)
	}
	if len(plan.RuntimeCommands) != 2 {
		t.Fatalf("runtime commands = %#v", plan.RuntimeCommands)
	}
	if !strings.Contains(strings.Join(plan.RuntimeCommands[0].Command, " "), "caddy reload") {
		t.Fatalf("caddy command = %#v", plan.RuntimeCommands[0])
	}
	if !strings.Contains(strings.Join(plan.RuntimeCommands[1].Command, " "), "sandcastle-route-broker.service") {
		t.Fatalf("broker command = %#v", plan.RuntimeCommands[1])
	}
}

func TestPlanDelete(t *testing.T) {
	plan, err := PlanDelete(config.LoadAdminFromEnv(), DeleteRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project != config.DefaultInfrastructureProject {
		t.Fatalf("Project = %q", plan.Project)
	}
	if len(plan.RuntimeInstances) != 2 {
		t.Fatalf("RuntimeInstances = %#v", plan.RuntimeInstances)
	}
	if plan.RuntimeInstances[0] != route.InfrastructureCaddyName || plan.RuntimeInstances[1] != RouteBrokerName {
		t.Fatalf("RuntimeInstances = %#v", plan.RuntimeInstances)
	}
}

func runtimeFileContent(plan CreatePlan, instance string, path string) string {
	for _, file := range plan.RuntimeFiles {
		if file.Instance == instance && file.Path == path {
			return file.Content
		}
	}
	return ""
}
