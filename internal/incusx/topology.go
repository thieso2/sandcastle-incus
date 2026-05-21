package incusx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type TopologyServer interface {
	UseProject(name string) TopologyResourceServer
}

type TopologyResourceServer interface {
	GetNetwork(name string) (*api.Network, string, error)
	GetStoragePoolVolume(pool string, volType string, name string) (*api.StorageVolume, string, error)
	GetInstance(name string) (*api.Instance, string, error)
	GetInstances(instanceType api.InstanceType) ([]api.Instance, error)
	GetInstanceFile(instanceName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error)
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
	tailscaleInstance := project.TailscaleInstanceName(request.IncusProject)
	topology := project.Topology{
		TailscaleInstance: tailscaleInstance,
		DurableVolumes:    map[string]bool{},
		Sidecars:          map[string]project.SidecarStatus{},
	}
	privateNetworkName := project.PrivateNetworkName(request.IncusProject)
	if _, _, err := projectServer.GetNetwork(privateNetworkName); err == nil {
		topology.PrivateNetworkPresent = true
	} else if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return project.Topology{}, fmt.Errorf("get private network %s: %w", privateNetworkName, err)
	}
	for _, volume := range []string{project.HomeVolumeName, project.WorkspaceVolumeName, project.CAVolumeName} {
		if _, _, err := projectServer.GetStoragePoolVolume(request.IncusProject, "custom", volume); err == nil {
			topology.DurableVolumes[volume] = true
		} else if !api.StatusErrorCheck(err, http.StatusNotFound) {
			return project.Topology{}, fmt.Errorf("get durable volume %s: %w", volume, err)
		}
	}
	for _, sidecar := range []string{tailscaleInstance, project.DNSName} {
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
	topology.DiagnosticFiles = diagnosticFiles(projectServer, request)
	return topology, nil
}

func diagnosticFiles(server TopologyResourceServer, request project.TopologyRequest) []project.DiagnosticFile {
	files := []project.DiagnosticFile{
		readDiagnosticFile(server, project.DNSName, "/etc/coredns/Corefile"),
	}
	domain := strings.Trim(strings.TrimSpace(request.Domain), ".")
	if domain != "" {
		files = append(files, readDiagnosticFile(server, project.DNSName, path.Join("/etc/coredns/zones", "db."+domain)))
	}
	for _, instance := range sandboxInstances(server) {
		files = append(files, readDiagnosticFile(server, instance.Name, sandbox.CaddyfilePath))
	}
	return files
}

func sandboxInstances(server TopologyResourceServer) []api.Instance {
	instances, err := server.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		return nil
	}
	sandboxes := []api.Instance{}
	for _, instance := range instances {
		if instance.Config[meta.KeyKind] == meta.KindSandbox {
			sandboxes = append(sandboxes, instance)
		}
	}
	sort.Slice(sandboxes, func(i, j int) bool {
		return sandboxes[i].Name < sandboxes[j].Name
	})
	return sandboxes
}

func readDiagnosticFile(server TopologyResourceServer, instance string, filePath string) project.DiagnosticFile {
	file := project.DiagnosticFile{Instance: instance, Path: filePath}
	reader, _, err := server.GetInstanceFile(instance, filePath)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			file.Error = "missing"
		} else {
			file.Error = err.Error()
		}
		return file
	}
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, 8192))
	if err != nil {
		file.Error = err.Error()
		return file
	}
	file.Content = string(content)
	return file
}

type sdkTopologyServer struct {
	inner incus.InstanceServer
}

func (s sdkTopologyServer) UseProject(name string) TopologyResourceServer {
	return s.inner.UseProject(name)
}
