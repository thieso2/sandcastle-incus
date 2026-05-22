package incusx

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/infra"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

func TestInfrastructureCreatorCreatesMissingResources(t *testing.T) {
	plan := infraPlanForTest(t)
	resourceServer := &fakeResourceServer{
		networks:  map[string]*api.Network{},
		volumes:   map[string]*api.StorageVolume{},
		instances: map[string]*api.Instance{},
	}
	server := &fakeCreateServer{resourceServer: resourceServer}
	creator := InfrastructureCreator{Server: server}

	if err := creator.CreateInfrastructure(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if server.createdProject == nil {
		t.Fatal("expected infrastructure project to be created")
	}
	if server.createdProject.Name != config.DefaultInfrastructureProject {
		t.Fatalf("created Incus project = %q", server.createdProject.Name)
	}
	if len(resourceServer.createdInstances) != 3 {
		t.Fatalf("created instances = %d, want 3", len(resourceServer.createdInstances))
	}
	if resourceServer.createdInstances[0].Name != route.InfrastructureCaddyName {
		t.Fatalf("first instance = %q", resourceServer.createdInstances[0].Name)
	}
	if resourceServer.createdInstances[1].Name != infra.RouteBrokerName {
		t.Fatalf("second instance = %q", resourceServer.createdInstances[1].Name)
	}
	if resourceServer.createdInstances[2].Name != infra.AuthAppName {
		t.Fatalf("third instance = %q", resourceServer.createdInstances[2].Name)
	}
	if resourceServer.createdFiles[route.InfrastructureCaddyName+":/etc/caddy/Caddyfile"] == "" {
		t.Fatal("expected bootstrap Caddyfile")
	}
	if resourceServer.createdFiles[infra.RouteBrokerName+":"+infra.RouteBrokerEnvPath] == "" {
		t.Fatal("expected route broker env file")
	}
	if resourceServer.createdFiles[infra.RouteBrokerName+":"+infra.RouteBrokerCertPath] == "" {
		t.Fatal("expected route broker certificate file")
	}
	if resourceServer.createdFiles[infra.RouteBrokerName+":"+infra.RouteBrokerKeyPath] == "" {
		t.Fatal("expected route broker key file")
	}
	if resourceServer.createdFiles[infra.RouteBrokerName+":"+infra.RouteBrokerBinaryPath] == "" {
		t.Fatal("expected route broker binary file")
	}
	if resourceServer.createdFiles[infra.AuthAppName+":"+infra.AuthAppEnvPath] == "" {
		t.Fatal("expected auth app env file")
	}
	if resourceServer.createdFiles[infra.AuthAppName+":"+infra.AuthAppUnitPath] == "" {
		t.Fatal("expected auth app unit file")
	}
	if resourceServer.createdFiles[infra.AuthAppName+":"+infra.AuthAppBinaryPath] == "" {
		t.Fatal("expected auth app binary file")
	}
	if len(resourceServer.execCommands) != 3 {
		t.Fatalf("exec commands = %#v", resourceServer.execCommands)
	}
	if resourceServer.execInstances[0] != route.InfrastructureCaddyName || !strings.Contains(strings.Join(resourceServer.execCommands[0], " "), "systemctl restart caddy") {
		t.Fatalf("first exec = %s %#v", resourceServer.execInstances[0], resourceServer.execCommands[0])
	}
	if resourceServer.execInstances[1] != infra.RouteBrokerName || !strings.Contains(strings.Join(resourceServer.execCommands[1], " "), "sandcastle-route-broker") {
		t.Fatalf("second exec = %s %#v", resourceServer.execInstances[1], resourceServer.execCommands[1])
	}
	if resourceServer.execInstances[2] != infra.AuthAppName || !strings.Contains(strings.Join(resourceServer.execCommands[2], " "), "sandcastle-auth-app") {
		t.Fatalf("third exec = %s %#v", resourceServer.execInstances[2], resourceServer.execCommands[2])
	}
}

func TestInfrastructureCreatorStartsExistingStoppedRuntime(t *testing.T) {
	plan := infraPlanForTest(t)
	plan.Instances[1].Config["security.privileged"] = "true"
	plan.Instances[1].Devices["incus-socket"] = infra.Device{
		"type":   "disk",
		"source": "/run/incus/unix.socket",
		"path":   infra.RouteBrokerIncusSocketPath,
	}
	resourceServer := &fakeResourceServer{
		networks: map[string]*api.Network{},
		volumes:  map[string]*api.StorageVolume{},
		instances: map[string]*api.Instance{
			route.InfrastructureCaddyName: {Name: route.InfrastructureCaddyName, Status: "Stopped", StatusCode: api.Stopped, InstancePut: api.InstancePut{Config: map[string]string{"image.os": "debian"}}},
			infra.RouteBrokerName:         {Name: infra.RouteBrokerName, Status: "Running", StatusCode: api.Running, InstancePut: api.InstancePut{Config: map[string]string{"image.os": "debian"}}},
			infra.AuthAppName:             {Name: infra.AuthAppName, Status: "Running", StatusCode: api.Running, InstancePut: api.InstancePut{Config: map[string]string{"image.os": "debian"}}},
		},
	}
	server := &fakeCreateServer{
		project:        &api.Project{Name: plan.Project},
		resourceServer: resourceServer,
	}
	creator := InfrastructureCreator{Server: server}

	if err := creator.CreateInfrastructure(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(resourceServer.startedInstances) != 1 {
		t.Fatalf("started instances = %#v", resourceServer.startedInstances)
	}
	if resourceServer.startedInstances[0] != route.InfrastructureCaddyName {
		t.Fatalf("started instance = %q", resourceServer.startedInstances[0])
	}
	if len(resourceServer.updatedInstances) != 3 {
		t.Fatalf("updated instances = %#v", resourceServer.updatedInstances)
	}
	routeBroker := resourceServer.instances[infra.RouteBrokerName]
	if routeBroker.Config["image.os"] != "debian" || routeBroker.Config["security.privileged"] != "true" {
		t.Fatalf("route broker config = %#v", routeBroker.Config)
	}
	if routeBroker.Devices["incus-socket"]["source"] != "/run/incus/unix.socket" {
		t.Fatalf("route broker devices = %#v", routeBroker.Devices)
	}
}

func TestInfrastructureDeleterDeletesRuntimeAndProject(t *testing.T) {
	plan := infra.DeletePlan{
		Project:          config.DefaultInfrastructureProject,
		RuntimeInstances: []string{route.InfrastructureCaddyName, infra.RouteBrokerName, infra.AuthAppName},
	}
	resourceServer := &fakeDeleteResourceServer{
		instances: map[string]*api.Instance{
			route.InfrastructureCaddyName: {Name: route.InfrastructureCaddyName, Status: "Running", StatusCode: api.Running},
			infra.RouteBrokerName:         {Name: infra.RouteBrokerName, Status: "Stopped", StatusCode: api.Stopped},
			infra.AuthAppName:             {Name: infra.AuthAppName, Status: "Stopped", StatusCode: api.Stopped},
		},
	}
	server := &fakeDeleteServer{resourceServer: resourceServer}
	deleter := InfrastructureDeleter{Server: server}
	if err := deleter.DeleteInfrastructure(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(resourceServer.deletedInstances) != 3 {
		t.Fatalf("deleted instances = %#v", resourceServer.deletedInstances)
	}
	if server.deletedProject != config.DefaultInfrastructureProject {
		t.Fatalf("deleted project = %q", server.deletedProject)
	}
}

func infraPlanForTest(t *testing.T) infra.CreatePlan {
	t.Helper()
	binaryPath := t.TempDir() + "/sandcastle"
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SANDCASTLE_BIN", binaryPath)
	plan, err := infra.PlanCreate(config.LoadAdminFromEnv(), infra.CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
