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
	images           []api.Image
	deletedImages    []string
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
func (s *fakeDeleteResourceServer) GetImages() ([]api.Image, error) {
	return append([]api.Image{}, s.images...), nil
}
func (s *fakeDeleteResourceServer) DeleteImage(fp string) (incus.Operation, error) {
	s.deletedImages = append(s.deletedImages, fp)
	return fakeOperation{}, nil
}
func (s *fakeDeleteResourceServer) GetProfiles() ([]api.Profile, error) { return nil, nil }
func (s *fakeDeleteResourceServer) GetProfile(name string) (*api.Profile, string, error) {
	return &api.Profile{Name: name}, "etag", nil
}
func (s *fakeDeleteResourceServer) UpdateProfile(name string, profile api.ProfilePut, ETag string) error {
	return nil
}
func (s *fakeDeleteResourceServer) DeleteProfile(name string) error { return nil }

func TestTenantDeleterPurgesProjectResources(t *testing.T) {
	plan, err := tenant.PlanDelete(config.LoadAdminFromEnv(), tenant.DeleteRequest{
		Reference: "acme",
		Purge:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Sidecars now live in the infra project, not the main project.
	// Infra project also has features.images=true with copied base images.
	infraServer := &fakeDeleteResourceServer{
		instances: map[string]*api.Instance{
			plan.SidecarInstances[0]: {Name: plan.SidecarInstances[0], StatusCode: api.Running},
			tenant.DNSName:           {Name: tenant.DNSName, StatusCode: api.Stopped},
		},
		images: []api.Image{{Fingerprint: "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"}},
	}
	mainServer := &fakeDeleteResourceServer{instances: map[string]*api.Instance{}}
	server := &fakeDeleteServer{
		resourceServer: mainServer,
		resources: map[string]*fakeDeleteResourceServer{
			plan.InfraProject:  infraServer,
			plan.NativeProject: {instances: map[string]*api.Instance{}},
		},
	}
	deleter := TenantDeleter{Server: server}

	if err := deleter.DeleteTenant(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(infraServer.stoppedInstances) != 1 || infraServer.stoppedInstances[0] != plan.SidecarInstances[0] {
		t.Fatalf("stopped instances = %#v", infraServer.stoppedInstances)
	}
	if len(infraServer.deletedInstances) != 2 {
		t.Fatalf("infra deleted instances = %#v", infraServer.deletedInstances)
	}
	if len(infraServer.deletedImages) != 1 {
		t.Fatalf("infra deleted images = %#v, want 1", infraServer.deletedImages)
	}
	if mainServer.deletedNetwork != tenant.PrivateNetworkName(plan.IncusProject) {
		t.Fatalf("deleted network = %q", mainServer.deletedNetwork)
	}
	if len(mainServer.deletedVolumes) != 3 {
		t.Fatalf("deleted volumes = %#v", mainServer.deletedVolumes)
	}
	// infra and native projects are always deleted; main project deleted on purge
	if server.deletedProjects[0] != plan.InfraProject {
		t.Fatalf("first deleted project = %q, want %q", server.deletedProjects[0], plan.InfraProject)
	}
	if server.deletedProjects[1] != plan.NativeProject {
		t.Fatalf("second deleted project = %q, want %q", server.deletedProjects[1], plan.NativeProject)
	}
	if server.deletedProjects[2] != plan.IncusProject {
		t.Fatalf("third deleted project = %q, want %q", server.deletedProjects[2], plan.IncusProject)
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
	server := &fakeDeleteServer{
		resourceServer: &fakeDeleteResourceServer{instances: map[string]*api.Instance{}},
		resources: map[string]*fakeDeleteResourceServer{
			plan.InfraProject:  {instances: map[string]*api.Instance{}},
			plan.NativeProject: {instances: map[string]*api.Instance{}},
		},
	}
	deleter := TenantDeleter{Server: server}

	if err := deleter.DeleteTenant(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(server.resourceServer.deletedVolumes) != 0 {
		t.Fatalf("deleted volumes = %#v", server.resourceServer.deletedVolumes)
	}
	// Infra and native projects are always purged; main project is kept without purge flag.
	if len(server.deletedProjects) != 2 {
		t.Fatalf("deleted projects = %#v, want infra+native only", server.deletedProjects)
	}
	if server.deletedProjects[0] != plan.InfraProject || server.deletedProjects[1] != plan.NativeProject {
		t.Fatalf("deleted projects = %#v", server.deletedProjects)
	}
}
