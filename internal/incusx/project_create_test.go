package incusx

import (
	"context"
	"net/http"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type fakeCreateServer struct {
	project        *api.Project
	createdProject *api.ProjectsPost
	updatedProject *api.ProjectPut
	resourceServer *fakeResourceServer
}

func (s *fakeCreateServer) GetProject(name string) (*api.Project, string, error) {
	if s.project == nil {
		return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
	}
	return s.project, "etag", nil
}

func (s *fakeCreateServer) CreateProject(project api.ProjectsPost) error {
	s.createdProject = &project
	return nil
}

func (s *fakeCreateServer) UpdateProject(name string, project api.ProjectPut, etag string) error {
	s.updatedProject = &project
	return nil
}

func (s *fakeCreateServer) UseProject(name string) ProjectResourceServer {
	return s.resourceServer
}

type fakeResourceServer struct {
	networks       map[string]*api.Network
	volumes        map[string]*api.StorageVolume
	createdNetwork *api.NetworksPost
	createdVolumes []api.StorageVolumesPost
}

func (s *fakeResourceServer) GetNetwork(name string) (*api.Network, string, error) {
	if network := s.networks[name]; network != nil {
		return network, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (s *fakeResourceServer) CreateNetwork(network api.NetworksPost) error {
	s.createdNetwork = &network
	s.networks[network.Name] = &api.Network{Name: network.Name}
	return nil
}

func (s *fakeResourceServer) GetStoragePoolVolume(pool string, volType string, name string) (*api.StorageVolume, string, error) {
	if volume := s.volumes[name]; volume != nil {
		return volume, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (s *fakeResourceServer) CreateStoragePoolVolume(pool string, volume api.StorageVolumesPost) error {
	s.createdVolumes = append(s.createdVolumes, volume)
	s.volumes[volume.Name] = &api.StorageVolume{Name: volume.Name, Type: volume.Type}
	return nil
}

func TestProjectCreatorCreatesMissingResources(t *testing.T) {
	plan := createPlanForTest(t)
	resourceServer := &fakeResourceServer{
		networks: map[string]*api.Network{},
		volumes:  map[string]*api.StorageVolume{},
	}
	server := &fakeCreateServer{resourceServer: resourceServer}
	creator := ProjectCreator{Server: server}

	if err := creator.CreateProject(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if server.createdProject == nil {
		t.Fatal("expected project to be created")
	}
	if server.createdProject.Name != "sc-alice-myproject" {
		t.Fatalf("created project = %q", server.createdProject.Name)
	}
	if resourceServer.createdNetwork == nil {
		t.Fatal("expected private network to be created")
	}
	if got := resourceServer.createdNetwork.Config["ipv4.address"]; got != "10.248.0.1/24" {
		t.Fatalf("network ipv4.address = %q", got)
	}
	if len(resourceServer.createdVolumes) != 3 {
		t.Fatalf("created volumes = %d, want 3", len(resourceServer.createdVolumes))
	}
}

func TestProjectCreatorUpdatesExistingProjectMetadata(t *testing.T) {
	plan := createPlanForTest(t)
	resourceServer := &fakeResourceServer{
		networks: map[string]*api.Network{plan.PrivateNetwork: {Name: plan.PrivateNetwork}},
		volumes: map[string]*api.StorageVolume{
			plan.HomeVolume:      {Name: plan.HomeVolume, Type: "custom"},
			plan.WorkspaceVolume: {Name: plan.WorkspaceVolume, Type: "custom"},
			plan.CAVolume:        {Name: plan.CAVolume, Type: "custom"},
		},
	}
	server := &fakeCreateServer{
		project: &api.Project{
			Name: plan.IncusProject,
			ProjectPut: api.ProjectPut{
				Description: "existing",
				Config:      api.ConfigMap{"features.images": "false"},
			},
		},
		resourceServer: resourceServer,
	}
	creator := ProjectCreator{Server: server}

	if err := creator.CreateProject(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if server.createdProject != nil {
		t.Fatal("did not expect project creation")
	}
	if server.updatedProject == nil {
		t.Fatal("expected project metadata update")
	}
	if server.updatedProject.Config["features.images"] != "false" {
		t.Fatalf("existing config was not preserved: %#v", server.updatedProject.Config)
	}
	if server.updatedProject.Config["user.sandcastle.owner"] != "alice" {
		t.Fatalf("managed metadata missing: %#v", server.updatedProject.Config)
	}
	if resourceServer.createdNetwork != nil {
		t.Fatal("did not expect existing network creation")
	}
	if len(resourceServer.createdVolumes) != 0 {
		t.Fatalf("created volumes = %d, want 0", len(resourceServer.createdVolumes))
	}
}

func createPlanForTest(t *testing.T) project.CreatePlan {
	t.Helper()
	plan, err := project.PlanCreate(config.LoadAdminFromEnv(), project.CreateRequest{
		Reference: "alice/myproject",
		Domain:    "myproject.project-tld",
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
