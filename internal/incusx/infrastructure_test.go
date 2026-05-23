package incusx

import (
	"bytes"
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
		networks:  fakeInfrastructureNetworks(),
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
	if resourceServer.createdFiles[infra.RouteBrokerName+":"+infra.RouteBrokerBinaryPath+".new"] == "" {
		t.Fatal("expected route broker binary file")
	}
	if resourceServer.createdFiles[infra.AuthAppName+":"+infra.AuthAppEnvPath] == "" {
		t.Fatal("expected auth app env file")
	}
	if resourceServer.createdFiles[infra.AuthAppName+":"+infra.AuthAppUnitPath] == "" {
		t.Fatal("expected auth app unit file")
	}
	if resourceServer.createdFiles[infra.AuthAppName+":"+infra.AuthAppBinaryPath+".new"] == "" {
		t.Fatal("expected auth app binary file")
	}
	if len(resourceServer.execCommands) != 5 {
		t.Fatalf("exec commands = %#v", resourceServer.execCommands)
	}
	if resourceServer.execInstances[0] != infra.RouteBrokerName || !strings.Contains(strings.Join(resourceServer.execCommands[0], " "), "mv -f") {
		t.Fatalf("first exec = %s %#v", resourceServer.execInstances[0], resourceServer.execCommands[0])
	}
	if resourceServer.execInstances[1] != infra.AuthAppName || !strings.Contains(strings.Join(resourceServer.execCommands[1], " "), "mv -f") {
		t.Fatalf("second exec = %s %#v", resourceServer.execInstances[1], resourceServer.execCommands[1])
	}
	if resourceServer.execInstances[2] != route.InfrastructureCaddyName || !strings.Contains(strings.Join(resourceServer.execCommands[2], " "), "systemctl restart caddy") {
		t.Fatalf("third exec = %s %#v", resourceServer.execInstances[2], resourceServer.execCommands[2])
	}
	if resourceServer.execInstances[3] != infra.RouteBrokerName || !strings.Contains(strings.Join(resourceServer.execCommands[3], " "), "sandcastle-route-broker") {
		t.Fatalf("fourth exec = %s %#v", resourceServer.execInstances[3], resourceServer.execCommands[3])
	}
	if resourceServer.execInstances[4] != infra.AuthAppName || !strings.Contains(strings.Join(resourceServer.execCommands[4], " "), "sandcastle-auth-app") {
		t.Fatalf("fifth exec = %s %#v", resourceServer.execInstances[4], resourceServer.execCommands[4])
	}
}

func TestInfrastructureCreatorVerboseLogsCommandDurations(t *testing.T) {
	plan := infraPlanForTest(t)
	plan.Instances = plan.Instances[:1]
	plan.RuntimeDirectories = plan.RuntimeDirectories[:1]
	plan.RuntimeFiles = nil
	plan.RuntimeBinaries = nil
	plan.RuntimeCommands = nil
	resourceServer := &fakeResourceServer{
		networks:  fakeInfrastructureNetworks(),
		volumes:   map[string]*api.StorageVolume{},
		instances: map[string]*api.Instance{},
	}
	server := &fakeCreateServer{resourceServer: resourceServer}
	var stderr bytes.Buffer
	creator := (InfrastructureCreator{Remote: "big", Server: server}).WithVerbose(true, &stderr)

	if err := creator.CreateInfrastructure(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	output := stderr.String()
	for _, want := range []string{
		"incus project create/update big:sc-infra ... done (",
		"incus launch/update sandcastle/base:latest big:sc-caddy --project sc-infra ... done (",
		"incus file mkdir big:sc-caddy/etc/caddy --project sc-infra ... done (",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("verbose output missing %q:\n%s", want, output)
		}
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
		networks: fakeInfrastructureNetworks(),
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

func TestInfrastructureDeleterPurgeDeletesTenantProjectsAndData(t *testing.T) {
	plan := infra.DeletePlan{
		Project:            config.DefaultInfrastructureProject,
		IncusProjectPrefix: "sc",
		RuntimeInstances:   []string{route.InfrastructureCaddyName},
		PurgeData:          true,
	}
	infraResources := &fakeDeleteResourceServer{
		instances: map[string]*api.Instance{
			route.InfrastructureCaddyName: {Name: route.InfrastructureCaddyName, Status: "Stopped", StatusCode: api.Stopped},
		},
	}
	tenantResources := &fakeDeleteResourceServer{
		instances: map[string]*api.Instance{
			"sc-dns": {Name: "sc-dns", Status: "Stopped", StatusCode: api.Stopped},
		},
	}
	server := &fakeDeleteServer{
		projects: []api.Project{
			{Name: "default"},
			{Name: config.DefaultInfrastructureProject},
			{Name: "sc-thieso2"},
			{Name: "other"},
		},
		resourceServer: infraResources,
		resources: map[string]*fakeDeleteResourceServer{
			config.DefaultInfrastructureProject: infraResources,
			"sc-thieso2":                        tenantResources,
		},
	}
	deleter := InfrastructureDeleter{Server: server}
	if err := deleter.DeleteInfrastructure(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if strings.Join(server.deletedProjects, ",") != "sc-infra,sc-thieso2" {
		t.Fatalf("deleted projects = %#v", server.deletedProjects)
	}
	if strings.Join(server.deletedPools, ",") != "sc-thieso2" {
		t.Fatalf("deleted pools = %#v", server.deletedPools)
	}
	if len(tenantResources.deletedInstances) != 1 || tenantResources.deletedInstances[0] != "sc-dns" {
		t.Fatalf("tenant deleted instances = %#v", tenantResources.deletedInstances)
	}
	if tenantResources.deletedNetwork != "sc-thieso2" {
		t.Fatalf("tenant deleted network = %q", tenantResources.deletedNetwork)
	}
	if strings.Join(tenantResources.deletedVolumes, ",") != "sc-home,sc-workspace,sc-ca" {
		t.Fatalf("tenant deleted volumes = %#v", tenantResources.deletedVolumes)
	}
}

func TestInfrastructureDeleterVerboseLogsCommandDurations(t *testing.T) {
	plan := infra.DeletePlan{
		Project:          config.DefaultInfrastructureProject,
		RuntimeInstances: []string{route.InfrastructureCaddyName},
	}
	resourceServer := &fakeDeleteResourceServer{
		instances: map[string]*api.Instance{
			route.InfrastructureCaddyName: {Name: route.InfrastructureCaddyName, Status: "Stopped", StatusCode: api.Stopped},
		},
	}
	server := &fakeDeleteServer{resourceServer: resourceServer}
	var stderr bytes.Buffer
	deleter := (InfrastructureDeleter{Remote: "big", Server: server}).WithVerbose(true, &stderr)

	if err := deleter.DeleteInfrastructure(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	output := stderr.String()
	for _, want := range []string{
		"incus delete big:sc-caddy --project sc-infra --force ... done (",
		"incus project delete big:sc-infra ... done (",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("verbose output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "...\n") {
		t.Fatalf("verbose output split command start and completion across lines:\n%s", output)
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

func fakeInfrastructureNetworks() map[string]*api.Network {
	return map[string]*api.Network{
		infra.InfrastructureNetworkName: {
			Name: infra.InfrastructureNetworkName,
			NetworkPut: api.NetworkPut{
				Config: map[string]string{
					"ipv4.address": "10.196.38.1/24",
				},
			},
		},
	}
}
