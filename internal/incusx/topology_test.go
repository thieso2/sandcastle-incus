package incusx

import (
	"context"
	"net/http"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type fakeTopologyServer struct {
	resource *fakeTopologyResource
}

func (s fakeTopologyServer) UseProject(name string) TopologyResourceServer {
	return s.resource
}

type fakeTopologyResource struct {
	networks  map[string]*api.Network
	volumes   map[string]*api.StorageVolume
	instances map[string]*api.Instance
}

func (r fakeTopologyResource) GetNetwork(name string) (*api.Network, string, error) {
	if network := r.networks[name]; network != nil {
		return network, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (r fakeTopologyResource) GetStoragePoolVolume(pool string, volType string, name string) (*api.StorageVolume, string, error) {
	if volume := r.volumes[name]; volume != nil {
		return volume, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (r fakeTopologyResource) GetInstance(name string) (*api.Instance, string, error) {
	if instance := r.instances[name]; instance != nil {
		return instance, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func TestTopologyStoreGetTopology(t *testing.T) {
	store := TopologyStore{Server: fakeTopologyServer{resource: &fakeTopologyResource{
		networks: map[string]*api.Network{project.PrivateNetworkName: {Name: project.PrivateNetworkName}},
		volumes: map[string]*api.StorageVolume{
			project.HomeVolumeName: {Name: project.HomeVolumeName},
			project.CAVolumeName:   {Name: project.CAVolumeName},
		},
		instances: map[string]*api.Instance{
			project.TailscaleName: {Name: project.TailscaleName, Status: "Stopped", StatusCode: api.Stopped},
			project.DNSName:       {Name: project.DNSName, Status: "Running", StatusCode: api.Running},
		},
	}}}
	topology, err := store.GetTopology(context.Background(), project.TopologyRequest{
		IncusProject: "sc-alice-myproject",
		StoragePool:  "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !topology.PrivateNetworkPresent {
		t.Fatal("private network should be present")
	}
	if !topology.DurableVolumes[project.HomeVolumeName] {
		t.Fatal("home volume should be present")
	}
	if topology.DurableVolumes[project.WorkspaceVolumeName] {
		t.Fatal("workspace volume should be missing")
	}
	if topology.Sidecars[project.TailscaleName].Running {
		t.Fatal("tailscale sidecar should be stopped")
	}
	if !topology.Sidecars[project.DNSName].Running {
		t.Fatal("dns sidecar should be running")
	}
}
