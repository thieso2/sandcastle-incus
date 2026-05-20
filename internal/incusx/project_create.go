package incusx

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type ProjectCreateServer interface {
	GetProject(name string) (*api.Project, string, error)
	CreateProject(project api.ProjectsPost) error
	UpdateProject(name string, project api.ProjectPut, ETag string) error
	UseProject(name string) ProjectResourceServer
}

type ProjectResourceServer interface {
	GetNetwork(name string) (*api.Network, string, error)
	CreateNetwork(network api.NetworksPost) error
	GetStoragePoolVolume(pool string, volType string, name string) (*api.StorageVolume, string, error)
	CreateStoragePoolVolume(pool string, volume api.StorageVolumesPost) error
	GetInstance(name string) (*api.Instance, string, error)
	CreateInstance(instance api.InstancesPost) (incus.Operation, error)
	UpdateInstanceState(name string, state api.InstanceStatePut, ETag string) (incus.Operation, error)
	CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error
}

type ProjectCreator struct {
	Remote     string
	ConfigPath string
	Server     ProjectCreateServer
}

func NewProjectCreator(remote string) ProjectCreator {
	return ProjectCreator{Remote: remote}
}

func (c ProjectCreator) CreateProject(ctx context.Context, plan project.CreatePlan) error {
	server := c.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(c.ConfigPath)
		if err != nil {
			return fmt.Errorf("load Incus config: %w", err)
		}
		remote := c.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkProjectServer{inner: instanceServer}
	}

	if err := ensureProject(server, plan); err != nil {
		return err
	}
	projectServer := server.UseProject(plan.IncusProject)
	if err := ensurePrivateNetwork(projectServer, plan); err != nil {
		return err
	}
	for _, volume := range volumeRequests(plan) {
		if err := ensureStorageVolume(projectServer, plan.StoragePool, volume); err != nil {
			return err
		}
	}
	for _, sidecar := range plan.Sidecars {
		if err := ensureSidecar(projectServer, sidecar); err != nil {
			return err
		}
	}
	if err := ensureDNSFiles(projectServer, plan); err != nil {
		return err
	}
	return nil
}

func ensureProject(server ProjectCreateServer, plan project.CreatePlan) error {
	existing, etag, err := server.GetProject(plan.IncusProject)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return server.CreateProject(api.ProjectsPost{
				Name: plan.IncusProject,
				ProjectPut: api.ProjectPut{
					Description: "Sandcastle project " + plan.Reference,
					Config:      api.ConfigMap(plan.ProjectMetadataConfig),
				},
			})
		}
		return fmt.Errorf("get Incus project %s: %w", plan.IncusProject, err)
	}
	config := mergeConfig(map[string]string(existing.Config), plan.ProjectMetadataConfig)
	if err := server.UpdateProject(plan.IncusProject, api.ProjectPut{
		Description: existing.Description,
		Config:      api.ConfigMap(config),
	}, etag); err != nil {
		return fmt.Errorf("update Incus project %s metadata: %w", plan.IncusProject, err)
	}
	return nil
}

func ensurePrivateNetwork(server ProjectResourceServer, plan project.CreatePlan) error {
	_, _, err := server.GetNetwork(plan.PrivateNetwork)
	if err == nil {
		return nil
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get private network %s: %w", plan.PrivateNetwork, err)
	}
	return server.CreateNetwork(networkRequest(plan))
}

func ensureStorageVolume(server ProjectResourceServer, pool string, volume api.StorageVolumesPost) error {
	_, _, err := server.GetStoragePoolVolume(pool, volume.Type, volume.Name)
	if err == nil {
		return nil
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get storage volume %s/%s: %w", pool, volume.Name, err)
	}
	return server.CreateStoragePoolVolume(pool, volume)
}

func ensureSidecar(server ProjectResourceServer, sidecar project.SidecarPlan) error {
	instance, _, err := server.GetInstance(sidecar.Name)
	if err == nil {
		if sidecar.Start && !instance.IsActive() {
			op, err := server.UpdateInstanceState(sidecar.Name, api.InstanceStatePut{
				Action:  "start",
				Timeout: -1,
			}, "")
			if err != nil {
				return fmt.Errorf("start sidecar %s: %w", sidecar.Name, err)
			}
			if err := op.Wait(); err != nil {
				return fmt.Errorf("wait for sidecar %s start: %w", sidecar.Name, err)
			}
		}
		return nil
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get sidecar %s: %w", sidecar.Name, err)
	}
	op, err := server.CreateInstance(sidecarRequest(sidecar))
	if err != nil {
		return fmt.Errorf("create sidecar %s: %w", sidecar.Name, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for sidecar %s create: %w", sidecar.Name, err)
	}
	return nil
}

func ensureDNSFiles(server ProjectResourceServer, plan project.CreatePlan) error {
	for _, directory := range []string{"/etc/coredns", "/etc/coredns/zones"} {
		err := server.CreateInstanceFile(plan.DNSInstance, directory, incus.InstanceFileArgs{
			Type: "directory",
			Mode: 0o755,
		})
		if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
			return fmt.Errorf("create DNS config directory %s: %w", directory, err)
		}
	}
	for _, file := range plan.DNSFiles {
		err := server.CreateInstanceFile(plan.DNSInstance, file.Path, incus.InstanceFileArgs{
			Content:   strings.NewReader(file.Content),
			Mode:      file.Mode,
			Type:      "file",
			WriteMode: "overwrite",
		})
		if err != nil {
			return fmt.Errorf("write DNS config file %s: %w", file.Path, err)
		}
	}
	return nil
}

func networkRequest(plan project.CreatePlan) api.NetworksPost {
	return api.NetworksPost{
		Name: plan.PrivateNetwork,
		Type: "bridge",
		NetworkPut: api.NetworkPut{
			Description: "Sandcastle private bridge for " + plan.Reference,
			Config: api.ConfigMap{
				"ipv4.address":      gatewayCIDR(plan.PrivateCIDR),
				"ipv4.nat":          "true",
				"ipv6.address":      "none",
				meta.KeyKind:        "network",
				meta.KeyOwner:       ownerFromPlan(plan),
				meta.KeyProject:     projectFromPlan(plan),
				meta.KeyPrivateCIDR: plan.PrivateCIDR,
				meta.KeyVersion:     "1",
			},
		},
	}
}

func volumeRequests(plan project.CreatePlan) []api.StorageVolumesPost {
	return []api.StorageVolumesPost{
		volumeRequest(plan, plan.HomeVolume, "Sandcastle home state for "+plan.Reference),
		volumeRequest(plan, plan.WorkspaceVolume, "Sandcastle workspace state for "+plan.Reference),
		volumeRequest(plan, plan.CAVolume, "Sandcastle project CA state for "+plan.Reference),
	}
}

func volumeRequest(plan project.CreatePlan, name string, description string) api.StorageVolumesPost {
	return api.StorageVolumesPost{
		Name:        name,
		Type:        "custom",
		ContentType: "filesystem",
		StorageVolumePut: api.StorageVolumePut{
			Description: description,
			Config: api.ConfigMap{
				meta.KeyKind:    "volume",
				meta.KeyOwner:   ownerFromPlan(plan),
				meta.KeyProject: projectFromPlan(plan),
				meta.KeyVersion: "1",
			},
		},
	}
}

func sidecarRequest(sidecar project.SidecarPlan) api.InstancesPost {
	return api.InstancesPost{
		Name:  sidecar.Name,
		Type:  "container",
		Start: sidecar.Start,
		Source: api.InstanceSource{
			Type:  "image",
			Alias: sidecar.ImageAlias,
		},
		InstancePut: api.InstancePut{
			Description: "Sandcastle " + sidecar.Role + " sidecar",
			Config:      api.ConfigMap(sidecar.Config),
			Devices:     devicesMap(sidecar.Devices),
			Profiles:    []string{},
		},
	}
}

func devicesMap(devices map[string]project.Device) api.DevicesMap {
	output := make(api.DevicesMap, len(devices))
	for name, device := range devices {
		output[name] = map[string]string(device)
	}
	return output
}

func gatewayCIDR(projectCIDR string) string {
	prefix, err := netip.ParsePrefix(projectCIDR)
	if err != nil {
		return projectCIDR
	}
	base := prefix.Masked().Addr().As4()
	base[3] = 1
	return netip.AddrFrom4(base).String() + fmt.Sprintf("/%d", prefix.Bits())
}

func mergeConfig(existing map[string]string, managed map[string]string) map[string]string {
	output := make(map[string]string, len(existing)+len(managed))
	for key, value := range existing {
		output[key] = value
	}
	for key, value := range managed {
		output[key] = value
	}
	return output
}

func ownerFromPlan(plan project.CreatePlan) string {
	ref, _, _ := splitReference(plan.Reference)
	return ref
}

func projectFromPlan(plan project.CreatePlan) string {
	_, name, _ := splitReference(plan.Reference)
	return name
}

func splitReference(value string) (string, string, bool) {
	for i, r := range value {
		if r == '/' {
			return value[:i], value[i+1:], true
		}
	}
	return "", "", false
}

type sdkProjectServer struct {
	inner incus.InstanceServer
}

func (s sdkProjectServer) GetProject(name string) (*api.Project, string, error) {
	return s.inner.GetProject(name)
}

func (s sdkProjectServer) CreateProject(project api.ProjectsPost) error {
	return s.inner.CreateProject(project)
}

func (s sdkProjectServer) UpdateProject(name string, project api.ProjectPut, etag string) error {
	return s.inner.UpdateProject(name, project, etag)
}

func (s sdkProjectServer) UseProject(name string) ProjectResourceServer {
	return sdkResourceServer{inner: s.inner.UseProject(name)}
}

type sdkResourceServer struct {
	inner incus.InstanceServer
}

func (s sdkResourceServer) GetNetwork(name string) (*api.Network, string, error) {
	return s.inner.GetNetwork(name)
}

func (s sdkResourceServer) CreateNetwork(network api.NetworksPost) error {
	return s.inner.CreateNetwork(network)
}

func (s sdkResourceServer) GetStoragePoolVolume(pool string, volType string, name string) (*api.StorageVolume, string, error) {
	return s.inner.GetStoragePoolVolume(pool, volType, name)
}

func (s sdkResourceServer) CreateStoragePoolVolume(pool string, volume api.StorageVolumesPost) error {
	return s.inner.CreateStoragePoolVolume(pool, volume)
}

func (s sdkResourceServer) GetInstance(name string) (*api.Instance, string, error) {
	return s.inner.GetInstance(name)
}

func (s sdkResourceServer) CreateInstance(instance api.InstancesPost) (incus.Operation, error) {
	return s.inner.CreateInstance(instance)
}

func (s sdkResourceServer) UpdateInstanceState(name string, state api.InstanceStatePut, etag string) (incus.Operation, error) {
	return s.inner.UpdateInstanceState(name, state, etag)
}

func (s sdkResourceServer) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
	return s.inner.CreateInstanceFile(instanceName, path, args)
}
