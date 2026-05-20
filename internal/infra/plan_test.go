package infra

import (
	"os"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

func TestPlanCreate(t *testing.T) {
	binaryPath := writeRuntimeBinaryForTest(t)
	t.Setenv("SANDCASTLE_BIN", binaryPath)
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
	if _, ok := plan.Instances[1].Devices["incus-socket"]; ok {
		t.Fatalf("route broker socket should be opt-in, devices = %#v", plan.Instances[1].Devices)
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
	if !strings.Contains(env, "SANDCASTLE_ROUTE_BROKER_LISTEN=':9443'") {
		t.Fatalf("env = %q", env)
	}
	for _, want := range []string{
		"SANDCASTLE_REMOTE='" + admin.Remote + "'",
		"SANDCASTLE_STORAGE_POOL='" + admin.StoragePool + "'",
		"SANDCASTLE_CIDR_POOL='" + admin.CIDRPool + "'",
		"SANDCASTLE_PROJECT_PREFIX='" + admin.ProjectPrefix + "'",
		"SANDCASTLE_INFRA_PROJECT='" + admin.InfrastructureProject + "'",
		"SANDCASTLE_BASE_IMAGE='" + admin.Images.Base + "'",
		"SANDCASTLE_AI_IMAGE='" + admin.Images.AI + "'",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("env missing %q:\n%s", want, env)
		}
	}
	if !strings.Contains(runtimeFileContent(plan, RouteBrokerName, RouteBrokerCertPath), "CERTIFICATE") {
		t.Fatal("expected route broker certificate PEM")
	}
	if !strings.Contains(runtimeFileContent(plan, RouteBrokerName, RouteBrokerKeyPath), "PRIVATE KEY") {
		t.Fatal("expected route broker private key PEM")
	}
	if len(plan.RuntimeBinaries) != 1 {
		t.Fatalf("runtime binaries = %#v", plan.RuntimeBinaries)
	}
	if plan.RuntimeBinaries[0].SourcePath != binaryPath || plan.RuntimeBinaries[0].TargetPath != RouteBrokerBinaryPath {
		t.Fatalf("runtime binary = %#v", plan.RuntimeBinaries[0])
	}
	if len(plan.RuntimeCommands) != 2 {
		t.Fatalf("runtime commands = %#v", plan.RuntimeCommands)
	}
	if !strings.Contains(strings.Join(plan.RuntimeCommands[0].Command, " "), "caddy reload") {
		t.Fatalf("caddy command = %#v", plan.RuntimeCommands[0])
	}
	if !strings.Contains(strings.Join(plan.RuntimeCommands[1].Command, " "), "admin route-broker serve") {
		t.Fatalf("broker command = %#v", plan.RuntimeCommands[1])
	}
}

func TestPlanCreateQuotesRouteBrokerEnv(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Remote = "local remote"
	admin.InfrastructureHost = "public.example.com"
	admin.Images.Base = "sandcastle/base:quote'test"
	plan, err := PlanCreate(admin, CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	env := runtimeFileContent(plan, RouteBrokerName, RouteBrokerEnvPath)
	for _, want := range []string{
		"SANDCASTLE_REMOTE='local remote'",
		"SANDCASTLE_INFRA_HOST='public.example.com'",
		"SANDCASTLE_BASE_IMAGE='sandcastle/base:quote'\"'\"'test'",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("env missing %q:\n%s", want, env)
		}
	}
}

func TestPlanCreateMountsRouteBrokerIncusSocketWhenConfigured(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.RouteBrokerIncusSocket = "/run/incus/unix.socket"
	plan, err := PlanCreate(admin, CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	routeBroker := plan.Instances[1]
	device := routeBroker.Devices["incus-socket"]
	if device == nil {
		t.Fatalf("route broker devices = %#v", routeBroker.Devices)
	}
	if device["type"] != "disk" || device["source"] != "/run/incus/unix.socket" || device["path"] != RouteBrokerIncusSocketPath {
		t.Fatalf("incus socket device = %#v", device)
	}
	if _, ok := plan.Instances[0].Devices["incus-socket"]; ok {
		t.Fatalf("caddy should not receive Incus socket, devices = %#v", plan.Instances[0].Devices)
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

func writeRuntimeBinaryForTest(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/sandcastle"
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
