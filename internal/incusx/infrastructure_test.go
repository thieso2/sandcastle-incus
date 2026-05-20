package incusx

import (
	"context"
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
		t.Fatalf("created project = %q", server.createdProject.Name)
	}
	if len(resourceServer.createdInstances) != 2 {
		t.Fatalf("created instances = %d, want 2", len(resourceServer.createdInstances))
	}
	if resourceServer.createdInstances[0].Name != route.InfrastructureCaddyName {
		t.Fatalf("first instance = %q", resourceServer.createdInstances[0].Name)
	}
	if resourceServer.createdInstances[1].Name != infra.RouteBrokerName {
		t.Fatalf("second instance = %q", resourceServer.createdInstances[1].Name)
	}
}

func TestInfrastructureCreatorStartsExistingStoppedRuntime(t *testing.T) {
	plan := infraPlanForTest(t)
	resourceServer := &fakeResourceServer{
		networks: map[string]*api.Network{},
		volumes:  map[string]*api.StorageVolume{},
		instances: map[string]*api.Instance{
			route.InfrastructureCaddyName: {Name: route.InfrastructureCaddyName, Status: "Stopped", StatusCode: api.Stopped},
			infra.RouteBrokerName:         {Name: infra.RouteBrokerName, Status: "Running", StatusCode: api.Running},
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
}

func infraPlanForTest(t *testing.T) infra.CreatePlan {
	t.Helper()
	plan, err := infra.PlanCreate(config.LoadAdminFromEnv(), infra.CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
