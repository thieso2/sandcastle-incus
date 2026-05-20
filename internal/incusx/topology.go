package incusx

import (
	"context"
	"fmt"
	"net/http"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type TopologyServer interface {
	UseProject(name string) TopologyResourceServer
}

type TopologyResourceServer interface {
	GetNetwork(name string) (*api.Network, string, error)
	GetStoragePoolVolume(pool string, volType string, name string) (*api.StorageVolume, string, error)
	GetInstance(name string) (*api.Instance, string, error)
}

type TopologyStore struct {
	Remote     string
	ConfigPath string
	Server     TopologyServer
}

func NewTopologyStore(remote string) TopologyStore {
	return TopologyStore{Remote: remote}
}

func (s TopologyStore) GetTopology(ctx context.Context, request project.TopologyRequest) (project.Topology, error) {
	server := s.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(s.ConfigPath)
		if err != nil {
			return project.Topology{}, fmt.Errorf("load Incus config: %w", err)
		}
		remote := s.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return project.Topology{}, fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkTopologyServer{inner: instanceServer}
	}
	projectServer := server.UseProject(request.IncusProject)
	topology := project.Topology{
		DurableVolumes: map[string]bool{},
		Sidecars:       map[string]project.SidecarStatus{},
	}
	if _, _, err := projectServer.GetNetwork(project.PrivateNetworkName); err == nil {
		topology.PrivateNetworkPresent = true
	} else if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return project.Topology{}, fmt.Errorf("get private network %s: %w", project.PrivateNetworkName, err)
	}
	for _, volume := range []string{project.HomeVolumeName, project.WorkspaceVolumeName, project.CAVolumeName} {
		if _, _, err := projectServer.GetStoragePoolVolume(request.StoragePool, "custom", volume); err == nil {
			topology.DurableVolumes[volume] = true
		} else if !api.StatusErrorCheck(err, http.StatusNotFound) {
			return project.Topology{}, fmt.Errorf("get durable volume %s: %w", volume, err)
		}
	}
	for _, sidecar := range []string{project.TailscaleName, project.DNSName} {
		instance, _, err := projectServer.GetInstance(sidecar)
		if err == nil {
			topology.Sidecars[sidecar] = project.SidecarStatus{
				Present: true,
				Running: instance.IsActive(),
				Status:  instance.Status,
			}
		} else if !api.StatusErrorCheck(err, http.StatusNotFound) {
			return project.Topology{}, fmt.Errorf("get sidecar %s: %w", sidecar, err)
		}
	}
	return topology, nil
}

type sdkTopologyServer struct {
	inner incus.InstanceServer
}

func (s sdkTopologyServer) UseProject(name string) TopologyResourceServer {
	return s.inner.UseProject(name)
}
