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
	t.Setenv("SANDCASTLE_ADMIN_BIN", binaryPath)
	admin := config.LoadAdminFromEnv()
	admin.LetsEncryptEmail = "ops@example.com"
	admin.AuthHostname = "auth.example.com"
	admin.AuthGitHubClientID = "github-client"
	admin.AuthGitHubClientSecret = "github-secret"
	admin.AuthAdminGitHubUsers = []string{"OctoCat", "hubot"}
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
	if plan.AuthAppInstance != AuthAppName {
		t.Fatalf("AuthAppInstance = %q", plan.AuthAppInstance)
	}
	if len(plan.Instances) != 3 {
		t.Fatalf("instances = %d, want 3", len(plan.Instances))
	}
	if plan.Instances[0].Name != route.InfrastructureCaddyName || plan.Instances[0].Role != "caddy" {
		t.Fatalf("caddy instance = %#v", plan.Instances[0])
	}
	if plan.Instances[1].Name != RouteBrokerName || plan.Instances[1].Role != "route-broker" {
		t.Fatalf("route broker instance = %#v", plan.Instances[1])
	}
	if plan.Instances[2].Name != AuthAppName || plan.Instances[2].Role != "auth-app" {
		t.Fatalf("auth app instance = %#v", plan.Instances[2])
	}
	for _, instance := range plan.Instances {
		eth0 := instance.Devices["eth0"]
		if eth0["type"] != "nic" || eth0["nictype"] != "bridged" || eth0["parent"] != InfrastructureNetworkName {
			t.Fatalf("%s eth0 device = %#v", instance.Name, eth0)
		}
		networkConfig := runtimeFileContent(plan, instance.Name, NetworkdEth0Path)
		if !strings.Contains(networkConfig, "Name=eth0") || !strings.Contains(networkConfig, "DHCP=yes") {
			t.Fatalf("%s network config = %q", instance.Name, networkConfig)
		}
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
	if !strings.Contains(runtimeFileContent(plan, route.InfrastructureCaddyName, "/etc/caddy/Caddyfile"), "email ops@example.com") {
		t.Fatalf("Caddyfile = %q", runtimeFileContent(plan, route.InfrastructureCaddyName, "/etc/caddy/Caddyfile"))
	}
	if !strings.Contains(runtimeFileContent(plan, route.InfrastructureCaddyName, "/etc/caddy/Caddyfile"), "auth.example.com") {
		t.Fatalf("Caddyfile missing auth host = %q", runtimeFileContent(plan, route.InfrastructureCaddyName, "/etc/caddy/Caddyfile"))
	}
	env := runtimeFileContent(plan, RouteBrokerName, RouteBrokerEnvPath)
	if !strings.Contains(env, "SANDCASTLE_ROUTE_BROKER_LISTEN=':9443'") {
		t.Fatalf("env = %q", env)
	}
	for _, want := range []string{
		"SANDCASTLE_REMOTE='" + admin.Remote + "'",
		"SANDCASTLE_STORAGE_POOL='" + admin.StoragePool + "'",
		"SANDCASTLE_CIDR_POOL='" + admin.CIDRPool + "'",
		"SANDCASTLE_INCUS_PROJECT_PREFIX='" + admin.IncusProjectPrefix + "'",
		"SANDCASTLE_INFRA_PROJECT='" + admin.InfrastructureProject + "'",
		"SANDCASTLE_LETSENCRYPT_EMAIL='ops@example.com'",
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
	authEnv := runtimeFileContent(plan, AuthAppName, AuthAppEnvPath)
	for _, want := range []string{
		"SANDCASTLE_AUTH_LISTEN=':9444'",
		"SANDCASTLE_AUTH_DB='" + AuthAppDatabasePath + "'",
		"SANDCASTLE_AUTH_HOSTNAME='auth.example.com'",
		"SANDCASTLE_AUTH_GITHUB_CLIENT_ID='github-client'",
		"SANDCASTLE_AUTH_GITHUB_CLIENT_SECRET='github-secret'",
		"SANDCASTLE_AUTH_ADMIN_GITHUB_USERS='OctoCat,hubot'",
		"SANDCASTLE_BASE_IMAGE='" + admin.Images.Base + "'",
		"SANDCASTLE_AI_IMAGE='" + admin.Images.AI + "'",
	} {
		if !strings.Contains(authEnv, want) {
			t.Fatalf("auth env missing %q:\n%s", want, authEnv)
		}
	}
	if !strings.Contains(runtimeFileContent(plan, AuthAppName, AuthAppUnitPath), "sandcastle-admin auth-app serve") {
		t.Fatalf("auth app unit = %q", runtimeFileContent(plan, AuthAppName, AuthAppUnitPath))
	}
	if len(plan.RuntimeBinaries) != 2 {
		t.Fatalf("runtime binaries = %#v", plan.RuntimeBinaries)
	}
	if plan.RuntimeBinaries[0].SourcePath != binaryPath || plan.RuntimeBinaries[0].TargetPath != RouteBrokerBinaryPath {
		t.Fatalf("runtime binary = %#v", plan.RuntimeBinaries[0])
	}
	if plan.RuntimeBinaries[1].SourcePath != binaryPath || plan.RuntimeBinaries[1].TargetPath != AuthAppBinaryPath {
		t.Fatalf("runtime binary = %#v", plan.RuntimeBinaries[1])
	}
	if len(plan.RuntimeCommands) != 3 {
		t.Fatalf("runtime commands = %#v", plan.RuntimeCommands)
	}
	for _, command := range plan.RuntimeCommands {
		joined := strings.Join(command.Command, " ")
		if !strings.Contains(joined, "systemctl restart systemd-networkd") || !strings.Contains(joined, "ip -4 addr show dev eth0") {
			t.Fatalf("%s command does not bootstrap DHCP: %#v", command.Instance, command)
		}
	}
	if !strings.Contains(runtimeFileContent(plan, RouteBrokerName, RouteBrokerUnitPath), "sandcastle-admin route-broker serve") {
		t.Fatalf("broker unit = %q", runtimeFileContent(plan, RouteBrokerName, RouteBrokerUnitPath))
	}
	if !strings.Contains(strings.Join(plan.RuntimeCommands[0].Command, " "), "systemctl restart caddy") {
		t.Fatalf("caddy command = %#v", plan.RuntimeCommands[0])
	}
	if !strings.Contains(strings.Join(plan.RuntimeCommands[1].Command, " "), "sandcastle-route-broker") {
		t.Fatalf("broker command = %#v", plan.RuntimeCommands[1])
	}
	if !strings.Contains(strings.Join(plan.RuntimeCommands[2].Command, " "), "sandcastle-auth-app") {
		t.Fatalf("auth command = %#v", plan.RuntimeCommands[2])
	}
}

func TestPlanCreateQuotesRouteBrokerEnv(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Remote = "local remote"
	admin.InfrastructureHost = "public.example.com"
	admin.LetsEncryptEmail = "ops+test@example.com"
	admin.AuthHostname = "auth.example.com"
	admin.Images.Base = "sandcastle/base:quote'test"
	plan, err := PlanCreate(admin, CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	env := runtimeFileContent(plan, RouteBrokerName, RouteBrokerEnvPath)
	for _, want := range []string{
		"SANDCASTLE_REMOTE='local remote'",
		"SANDCASTLE_INFRA_HOST='public.example.com'",
		"SANDCASTLE_LETSENCRYPT_EMAIL='ops+test@example.com'",
		"SANDCASTLE_BASE_IMAGE='sandcastle/base:quote'\"'\"'test'",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("env missing %q:\n%s", want, env)
		}
	}
	authEnv := runtimeFileContent(plan, AuthAppName, AuthAppEnvPath)
	if !strings.Contains(authEnv, "SANDCASTLE_AUTH_HOSTNAME='auth.example.com'") {
		t.Fatalf("auth env = %q", authEnv)
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
	if routeBroker.Config["security.privileged"] != "true" {
		t.Fatalf("route broker with host Incus socket must be privileged, config = %#v", routeBroker.Config)
	}
	authApp := plan.Instances[2]
	authDevice := authApp.Devices["incus-socket"]
	if authDevice == nil {
		t.Fatalf("auth app devices = %#v", authApp.Devices)
	}
	if authDevice["type"] != "disk" || authDevice["source"] != "/run/incus/unix.socket" || authDevice["path"] != RouteBrokerIncusSocketPath {
		t.Fatalf("auth app incus socket device = %#v", authDevice)
	}
	if authApp.Config["security.privileged"] != "true" {
		t.Fatalf("auth app with host Incus socket must be privileged, config = %#v", authApp.Config)
	}
	if plan.Instances[0].Config["security.privileged"] == "true" {
		t.Fatalf("caddy should not be privileged, config = %#v", plan.Instances[0].Config)
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
	if len(plan.RuntimeInstances) != 3 {
		t.Fatalf("RuntimeInstances = %#v", plan.RuntimeInstances)
	}
	if plan.RuntimeInstances[0] != route.InfrastructureCaddyName || plan.RuntimeInstances[1] != RouteBrokerName || plan.RuntimeInstances[2] != AuthAppName {
		t.Fatalf("RuntimeInstances = %#v", plan.RuntimeInstances)
	}
}

func TestPlanDeleteRejectsInvalidExplicitProject(t *testing.T) {
	_, err := PlanDelete(config.LoadAdminFromEnv(), DeleteRequest{Project: "bad_project"})
	if err == nil {
		t.Fatal("expected error")
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
