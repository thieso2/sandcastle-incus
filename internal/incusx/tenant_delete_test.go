package incusx

import (
	"context"
	"net/http"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/config"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type fakeDeleteServer struct {
	projects        []api.Project
	deletedProject  string
	deletedProjects []string
	deletedPool     string
	deletedPools    []string
	resourceServer  *fakeDeleteResourceServer
	resources       map[string]*fakeDeleteResourceServer
}

func (s *fakeDeleteServer) GetProjects() ([]api.Project, error) {
	return append([]api.Project{}, s.projects...), nil
}

func (s *fakeDeleteServer) DeleteProject(name string) error {
	s.deletedProject = name
	s.deletedProjects = append(s.deletedProjects, name)
	return nil
}

func (s *fakeDeleteServer) DeleteStoragePool(name string) error {
	s.deletedPool = name
	s.deletedPools = append(s.deletedPools, name)
	return nil
}

func (s *fakeDeleteServer) UseProject(name string) TenantDeleteResourceServer {
	if s.resources != nil {
		if resource := s.resources[name]; resource != nil {
			return resource
		}
	}
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
func (s *fakeDeleteResourceServer) GetImages() ([]api.Image, error) { return nil, nil }
func (s *fakeDeleteResourceServer) DeleteImage(fp string) (incus.Operation, error) {
	return fakeOperation{}, nil
}
func (s *fakeDeleteResourceServer) GetProfiles() ([]api.Profile, error) { return nil, nil }
func (s *fakeDeleteResourceServer) DeleteProfile(name string) error     { return nil }

func TestTenantDeleterPurgesProjectResources(t *testing.T) {
	plan, err := tenant.PlanDelete(config.LoadAdminFromEnv(), tenant.DeleteRequest{
		Reference: "acme",
		Purge:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	resourceServer := &fakeDeleteResourceServer{
		instances: map[string]*api.Instance{
			plan.SidecarInstances[0]: {Name: plan.SidecarInstances[0], StatusCode: api.Running},
			tenant.DNSName:           {Name: tenant.DNSName, StatusCode: api.Stopped},
		},
	}
	server := &fakeDeleteServer{resourceServer: resourceServer}
	deleter := TenantDeleter{Server: server}

	if err := deleter.DeleteTenant(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(resourceServer.stoppedInstances) != 1 || resourceServer.stoppedInstances[0] != plan.SidecarInstances[0] {
		t.Fatalf("stopped instances = %#v", resourceServer.stoppedInstances)
	}
	if len(resourceServer.deletedInstances) != 2 {
		t.Fatalf("deleted instances = %#v", resourceServer.deletedInstances)
	}
	if resourceServer.deletedNetwork != tenant.PrivateNetworkName(plan.IncusProject) {
		t.Fatalf("deleted network = %q", resourceServer.deletedNetwork)
	}
	if len(resourceServer.deletedVolumes) != 3 {
		t.Fatalf("deleted volumes = %#v", resourceServer.deletedVolumes)
	}
	if server.deletedProject != "sc-acme" {
		t.Fatalf("deleted project = %q", server.deletedProject)
	}
}

func TestTenantDeleterPreservesDurableStateWithoutPurge(t *testing.T) {
	plan, err := tenant.PlanDelete(config.LoadAdminFromEnv(), tenant.DeleteRequest{
		Reference: "acme",
		Purge:     false,
	})
	if err != nil {
		t.Fatal(err)
	}
	resourceServer := &fakeDeleteResourceServer{instances: map[string]*api.Instance{}}
	server := &fakeDeleteServer{resourceServer: resourceServer}
	deleter := TenantDeleter{Server: server}

	if err := deleter.DeleteTenant(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(resourceServer.deletedVolumes) != 0 {
		t.Fatalf("deleted volumes = %#v", resourceServer.deletedVolumes)
	}
	if server.deletedProject != "" {
		t.Fatalf("deleted project = %q", server.deletedProject)
	}
}
