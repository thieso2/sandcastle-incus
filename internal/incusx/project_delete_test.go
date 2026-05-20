package incusx

import (
	"context"
	"net/http"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type fakeDeleteServer struct {
	deletedProject string
	resourceServer *fakeDeleteResourceServer
}

func (s *fakeDeleteServer) DeleteProject(name string) error {
	s.deletedProject = name
	return nil
}

func (s *fakeDeleteServer) UseProject(name string) ProjectDeleteResourceServer {
	return s.resourceServer
}

type fakeDeleteResourceServer struct {
	instances        map[string]*api.Instance
	deletedInstances []string
	stoppedInstances []string
	deletedNetwork   string
	deletedVolumes   []string
}

func (s *fakeDeleteResourceServer) GetInstances(instanceType api.InstanceType) ([]api.Instance, error) {
	var result []api.Instance
	for _, instance := range s.instances {
		result = append(result, *instance)
	}
	return result, nil
}

func (s *fakeDeleteResourceServer) GetInstance(name string) (*api.Instance, string, error) {
	if instance := s.instances[name]; instance != nil {
		return instance, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (s *fakeDeleteResourceServer) UpdateInstanceState(name string, state api.InstanceStatePut, etag string) (incus.Operation, error) {
	s.stoppedInstances = append(s.stoppedInstances, name)
	if s.instances[name] != nil {
		s.instances[name].StatusCode = api.Stopped
	}
	return fakeOperation{}, nil
}

func (s *fakeDeleteResourceServer) DeleteInstance(name string) (incus.Operation, error) {
	s.deletedInstances = append(s.deletedInstances, name)
	return fakeOperation{}, nil
}

func (s *fakeDeleteResourceServer) DeleteNetwork(name string) error {
	s.deletedNetwork = name
	return nil
}

func (s *fakeDeleteResourceServer) DeleteStoragePoolVolume(pool string, volType string, name string) error {
	s.deletedVolumes = append(s.deletedVolumes, name)
	return nil
}

func TestProjectDeleterPurgesProjectResources(t *testing.T) {
	plan, err := project.PlanDelete(config.LoadAdminFromEnv(), project.DeleteRequest{
		Reference: "alice/myproject",
		Purge:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	resourceServer := &fakeDeleteResourceServer{
		instances: map[string]*api.Instance{
			plan.SidecarInstances[0]: {Name: plan.SidecarInstances[0], StatusCode: api.Running},
			project.DNSName:          {Name: project.DNSName, StatusCode: api.Stopped},
		},
	}
	server := &fakeDeleteServer{resourceServer: resourceServer}
	deleter := ProjectDeleter{Server: server}

	if err := deleter.DeleteProject(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(resourceServer.stoppedInstances) != 1 || resourceServer.stoppedInstances[0] != plan.SidecarInstances[0] {
		t.Fatalf("stopped instances = %#v", resourceServer.stoppedInstances)
	}
	if len(resourceServer.deletedInstances) != 2 {
		t.Fatalf("deleted instances = %#v", resourceServer.deletedInstances)
	}
	if resourceServer.deletedNetwork != project.PrivateNetworkName(plan.IncusProject) {
		t.Fatalf("deleted network = %q", resourceServer.deletedNetwork)
	}
	if len(resourceServer.deletedVolumes) != 3 {
		t.Fatalf("deleted volumes = %#v", resourceServer.deletedVolumes)
	}
	if server.deletedProject != "sc-alice-myproject" {
		t.Fatalf("deleted project = %q", server.deletedProject)
	}
}

func TestProjectDeleterPreservesDurableStateWithoutPurge(t *testing.T) {
	plan, err := project.PlanDelete(config.LoadAdminFromEnv(), project.DeleteRequest{
		Reference: "alice/myproject",
		Purge:     false,
	})
	if err != nil {
		t.Fatal(err)
	}
	resourceServer := &fakeDeleteResourceServer{instances: map[string]*api.Instance{}}
	server := &fakeDeleteServer{resourceServer: resourceServer}
	deleter := ProjectDeleter{Server: server}

	if err := deleter.DeleteProject(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(resourceServer.deletedVolumes) != 0 {
		t.Fatalf("deleted volumes = %#v", resourceServer.deletedVolumes)
	}
	if server.deletedProject != "" {
		t.Fatalf("deleted project = %q", server.deletedProject)
	}
}
