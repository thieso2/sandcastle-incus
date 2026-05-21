package incusx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type ProjectCreateServer interface {
	GetProject(name string) (*api.Project, string, error)
	CreateProject(project api.ProjectsPost) error
	UpdateProject(name string, project api.ProjectPut, ETag string) error
	UseProject(name string) ProjectResourceServer
	GetStoragePool(name string) (*api.StoragePool, string, error)
	CreateStoragePool(pool api.StoragePoolsPost) error
	GetImage(ref string) (*api.Image, string, error)
	GetImageAlias(name string) (*api.ImageAliasesEntry, string, error)
	imageServer() incus.ImageServer
}

type ProjectResourceServer interface {
	GetNetwork(name string) (*api.Network, string, error)
	CreateNetwork(network api.NetworksPost) error
	GetStoragePoolVolume(pool string, volType string, name string) (*api.StorageVolume, string, error)
	CreateStoragePoolVolume(pool string, volume api.StorageVolumesPost) error
	CreateStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error
	GetProfile(name string) (*api.Profile, string, error)
	CreateProfile(profile api.ProfilesPost) error
	UpdateProfile(name string, profile api.ProfilePut, ETag string) error
	GetInstance(name string) (*api.Instance, string, error)
	CreateInstance(instance api.InstancesPost) (incus.Operation, error)
	UpdateInstanceState(name string, state api.InstanceStatePut, ETag string) (incus.Operation, error)
	CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
	GetImage(ref string) (*api.Image, string, error)
	GetImageAlias(name string) (*api.ImageAliasesEntry, string, error)
	CreateImageAlias(alias api.ImageAliasesPost) error
	CopyImageFrom(source ProjectCreateServer, image api.Image, aliases []api.ImageAlias) (incus.RemoteOperation, error)
}

type ProjectCreator struct {
	Remote     string
	ConfigPath string
	Server     ProjectCreateServer
	Log        func(string)
}

func NewProjectCreator(remote string) ProjectCreator {
	return ProjectCreator{Remote: remote}
}

func (c ProjectCreator) WithVerbose(enabled bool, w io.Writer) ProjectCreator {
	if enabled {
		c.Log = func(msg string) { fmt.Fprintln(w, "[project-create] "+msg) }
	}
	return c
}

func (c ProjectCreator) log(msg string) {
	if c.Log != nil {
		c.Log(msg)
	}
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

	c.log("ensure project " + plan.IncusProject)
	if err := ensureProject(server, plan); err != nil {
		return err
	}
	c.log("ensure storage pool " + plan.StoragePool)
	if err := ensureStoragePool(server, plan); err != nil {
		return err
	}
	projectServer := server.UseProject(plan.IncusProject)
	c.log("ensure project images")
	if err := ensureProjectImages(server, projectServer, plan.ImageAliases); err != nil {
		return err
	}
	c.log("ensure private network " + plan.PrivateNetwork)
	if err := ensurePrivateNetwork(projectServer, plan); err != nil {
		return err
	}
	for _, volume := range volumeRequests(plan) {
		c.log("ensure storage volume " + volume.Name)
		if err := ensureStorageVolume(projectServer, plan.StoragePool, volume); err != nil {
			return err
		}
	}
	c.log("ensure project CA")
	if err := ensureProjectCA(projectServer, plan); err != nil {
		return err
	}
	for _, sidecar := range plan.Sidecars {
		c.log("ensure sidecar " + sidecar.Name + " (image: " + sidecar.ImageAlias + ")")
		if err := ensureSidecar(projectServer, sidecar); err != nil {
			return err
		}
		c.log("configure network for sidecar " + sidecar.Name)
		if err := configureSidecarNetwork(projectServer, sidecar, plan.PrivateCIDR); err != nil {
			return err
		}
	}
	c.log("ensure container profile")
	if err := ensureContainerProfile(projectServer, plan); err != nil {
		return err
	}
	c.log("ensure DNS files")
	if err := ensureDNSFiles(projectServer, plan); err != nil {
		return err
	}
	c.log("restart CoreDNS")
	if err := restartCoreDNS(projectServer); err != nil {
		return err
	}
	c.log("done")
	return nil
}

func ensureProject(server ProjectCreateServer, plan project.CreatePlan) error {
	existing, etag, err := server.GetProject(plan.IncusProject)
	if err != nil {
		// 404 = project doesn't exist.
		// 403 = Incus fine-grained auth: the calling cert hasn't been granted access
		//       to this project yet (which also means it doesn't exist from our perspective).
		// In both cases, attempt to create it.
		if api.StatusErrorCheck(err, http.StatusNotFound) || api.StatusErrorCheck(err, http.StatusForbidden) {
			cfg := mergeConfig(isolatedProjectFeatureConfig(), plan.ProjectMetadataConfig)
			return server.CreateProject(api.ProjectsPost{
				Name: plan.IncusProject,
				ProjectPut: api.ProjectPut{
					Description: "Sandcastle project " + plan.Reference,
					Config:      api.ConfigMap(cfg),
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

func isolatedProjectFeatureConfig() map[string]string {
	return map[string]string{
		"features.images":          "true",
		"features.profiles":        "true",
		"features.storage.buckets": "true",
		"features.storage.volumes": "true",
	}
}

func ensureProjectImages(source ProjectCreateServer, target ProjectResourceServer, aliases []string) error {
	for _, aliasName := range aliases {
		if err := ensureProjectImage(source, target, aliasName); err != nil {
			return err
		}
	}
	return nil
}

func ensureProjectImage(source ProjectCreateServer, target ProjectResourceServer, aliasName string) error {
	sourceAlias, _, err := source.GetImageAlias(aliasName)
	if err != nil {
		return fmt.Errorf("get source image alias %s: %w", aliasName, err)
	}
	sourceImage, _, err := source.GetImage(sourceAlias.Target)
	if err != nil {
		return fmt.Errorf("get source image %s target %s: %w", aliasName, sourceAlias.Target, err)
	}
	targetAlias, _, err := target.GetImageAlias(aliasName)
	if err == nil {
		if targetAlias.Target == sourceImage.Fingerprint {
			return nil
		}
		return fmt.Errorf("project image alias %s targets %s, want %s", aliasName, targetAlias.Target, sourceImage.Fingerprint)
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get project image alias %s: %w", aliasName, err)
	}
	if _, _, err := target.GetImage(sourceImage.Fingerprint); err == nil {
		if err := target.CreateImageAlias(api.ImageAliasesPost{
			ImageAliasesEntry: api.ImageAliasesEntry{
				Name: aliasName,
				Type: imageAliasType(sourceAlias),
				ImageAliasesEntryPut: api.ImageAliasesEntryPut{
					Description: sourceAlias.Description,
					Target:      sourceImage.Fingerprint,
				},
			},
		}); err != nil {
			return fmt.Errorf("create project image alias %s: %w", aliasName, err)
		}
		return nil
	} else if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get project image %s: %w", sourceImage.Fingerprint, err)
	}
	remoteOp, err := target.CopyImageFrom(source, *sourceImage, []api.ImageAlias{{
		Name:        aliasName,
		Description: sourceAlias.Description,
	}})
	if err != nil {
		return fmt.Errorf("copy image %s into project: %w", aliasName, err)
	}
	if err := remoteOp.Wait(); err != nil {
		return fmt.Errorf("wait for image %s copy into project: %w", aliasName, err)
	}
	return nil
}

func imageAliasType(alias *api.ImageAliasesEntry) string {
	if alias.Type != "" {
		return alias.Type
	}
	return "container"
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

func ensureProjectCA(server ProjectResourceServer, plan project.CreatePlan) error {
	if len(plan.ProjectCA.CertificatePEM) == 0 || len(plan.ProjectCA.PrivateKeyPEM) == 0 {
		return fmt.Errorf("project CA material is missing")
	}
	if err := server.CreateStorageVolumeFile(plan.StoragePool, "custom", plan.CAVolume, plan.ProjectCA.CertificatePath, incus.InstanceFileArgs{
		Content:   strings.NewReader(string(plan.ProjectCA.CertificatePEM)),
		Type:      "file",
		Mode:      0o644,
		WriteMode: "overwrite",
	}); err != nil {
		return fmt.Errorf("write project CA certificate: %w", err)
	}
	if err := server.CreateStorageVolumeFile(plan.StoragePool, "custom", plan.CAVolume, plan.ProjectCA.PrivateKeyPath, incus.InstanceFileArgs{
		Content:   strings.NewReader(string(plan.ProjectCA.PrivateKeyPEM)),
		Type:      "file",
		Mode:      0o600,
		WriteMode: "overwrite",
	}); err != nil {
		return fmt.Errorf("write project CA private key: %w", err)
	}
	return nil
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

type coreDNSRestarter interface {
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}

func restartCoreDNS(server coreDNSRestarter) error {
	var stderr strings.Builder
	dataDone := make(chan bool)
	op, err := server.ExecInstance(project.DNSName, api.InstanceExecPost{
		Command: []string{"/bin/sh", "-c", strings.Join([]string{
			"pkill -x coredns >/dev/null 2>&1 || true",
			"systemd-run --unit=coredns --collect -- /usr/local/bin/coredns -conf /etc/coredns/Corefile",
			"sleep 0.2",
			"pgrep -x coredns >/dev/null 2>&1",
		}, " && ")},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		Stderr:   &stderr,
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("restart CoreDNS in %s: %w", project.DNSName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for CoreDNS restart in %s (stderr: %s): %w", project.DNSName, stderr.String(), err)
	}
	<-dataDone
	return nil
}

func configureSidecarNetwork(server ProjectResourceServer, sidecar project.SidecarPlan, privateCIDR string) error {
	if sidecar.Address == "" || privateCIDR == "" {
		return nil
	}
	prefix, err := netip.ParsePrefix(privateCIDR)
	if err != nil {
		return fmt.Errorf("parse private CIDR %s: %w", privateCIDR, err)
	}
	ipWithPrefix := sidecar.Address + fmt.Sprintf("/%d", prefix.Bits())
	gateway, err := gatewayIPFromCIDR(privateCIDR)
	if err != nil {
		return err
	}
	var stderr strings.Builder
	dataDone := make(chan bool)
	cmds := []string{
		"/usr/sbin/ip link set eth0 up",
		"/usr/sbin/ip addr add " + ipWithPrefix + " dev eth0 2>/dev/null || true",
		"/usr/sbin/ip route add default via " + gateway + " 2>/dev/null || true",
	}
	if sidecar.Role == "dns" {
		cmds = append(cmds,
			"systemctl stop tailscale.service 2>/dev/null || true",
			"systemctl mask tailscale.service",
		)
	}
	op, err := server.ExecInstance(sidecar.Name, api.InstanceExecPost{
		Command:   []string{"/bin/sh", "-c", strings.Join(cmds, " && ")},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		Stderr:   &stderr,
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("configure network for sidecar %s: %w", sidecar.Name, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for sidecar %s network config (stderr: %s): %w", sidecar.Name, stderr.String(), err)
	}
	<-dataDone
	return nil
}

func ensureStoragePool(server ProjectCreateServer, plan project.CreatePlan) error {
	_, _, err := server.GetStoragePool(plan.StoragePool)
	if err == nil {
		return nil
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get storage pool %s: %w", plan.StoragePool, err)
	}
	adminPool, _, err := server.GetStoragePool(plan.AdminStoragePool)
	if err != nil {
		return fmt.Errorf("get admin storage pool %s: %w", plan.AdminStoragePool, err)
	}
	poolConfig := api.ConfigMap{
		meta.KeyKind:    "pool",
		meta.KeyOwner:   ownerFromPlan(plan),
		meta.KeyProject: projectFromPlan(plan),
		meta.KeyVersion: "1",
	}
	if source := adminPool.Config["source"]; source != "" {
		poolConfig["source"] = source + "/" + plan.StoragePool
	}
	return server.CreateStoragePool(api.StoragePoolsPost{
		Name:   plan.StoragePool,
		Driver: adminPool.Driver,
		StoragePoolPut: api.StoragePoolPut{
			Description: "Sandcastle per-project storage for " + plan.Reference,
			Config:      poolConfig,
		},
	})
}

func ensureContainerProfile(server ProjectResourceServer, plan project.CreatePlan) error {
	profilePut := api.ProfilePut{
		Description: "Sandcastle container defaults for " + plan.Reference,
		Config: api.ConfigMap{
			meta.KeyKind:    "profile",
			meta.KeyOwner:   ownerFromPlan(plan),
			meta.KeyProject: projectFromPlan(plan),
			meta.KeyVersion: "1",
		},
		Devices: api.DevicesMap{
			"root": {
				"type": "disk",
				"pool": plan.StoragePool,
				"path": "/",
			},
			"eth0": {
				"type":    "nic",
				"nictype": "bridged",
				"parent":  plan.PrivateNetwork,
			},
		},
	}
	_, etag, err := server.GetProfile("container")
	if err == nil {
		return server.UpdateProfile("container", profilePut, etag)
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get container profile: %w", err)
	}
	return server.CreateProfile(api.ProfilesPost{
		Name:       "container",
		ProfilePut: profilePut,
	})
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

func gatewayIPFromCIDR(cidr string) (string, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return "", fmt.Errorf("parse CIDR %s: %w", cidr, err)
	}
	base := prefix.Masked().Addr().As4()
	base[3] = 1
	return netip.AddrFrom4(base).String(), nil
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

func (s sdkProjectServer) GetImage(ref string) (*api.Image, string, error) {
	return s.inner.GetImage(ref)
}

func (s sdkProjectServer) GetImageAlias(name string) (*api.ImageAliasesEntry, string, error) {
	return s.inner.GetImageAlias(name)
}

func (s sdkProjectServer) imageServer() incus.ImageServer {
	return s.inner
}

func (s sdkProjectServer) UpdateProject(name string, project api.ProjectPut, etag string) error {
	return s.inner.UpdateProject(name, project, etag)
}

func (s sdkProjectServer) UseProject(name string) ProjectResourceServer {
	return sdkResourceServer{inner: s.inner.UseProject(name)}
}

func (s sdkProjectServer) GetStoragePool(name string) (*api.StoragePool, string, error) {
	return s.inner.GetStoragePool(name)
}

func (s sdkProjectServer) CreateStoragePool(pool api.StoragePoolsPost) error {
	return s.inner.CreateStoragePool(pool)
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

func (s sdkResourceServer) CreateStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error {
	return s.inner.CreateStorageVolumeFile(pool, volumeType, volumeName, filePath, args)
}

func (s sdkResourceServer) GetProfile(name string) (*api.Profile, string, error) {
	return s.inner.GetProfile(name)
}

func (s sdkResourceServer) CreateProfile(profile api.ProfilesPost) error {
	return s.inner.CreateProfile(profile)
}

func (s sdkResourceServer) UpdateProfile(name string, profile api.ProfilePut, etag string) error {
	return s.inner.UpdateProfile(name, profile, etag)
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

func (s sdkResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	return s.inner.ExecInstance(instanceName, exec, args)
}

func (s sdkResourceServer) GetImage(ref string) (*api.Image, string, error) {
	return s.inner.GetImage(ref)
}

func (s sdkResourceServer) GetImageAlias(name string) (*api.ImageAliasesEntry, string, error) {
	return s.inner.GetImageAlias(name)
}

func (s sdkResourceServer) CreateImageAlias(alias api.ImageAliasesPost) error {
	return s.inner.CreateImageAlias(alias)
}

func (s sdkResourceServer) CopyImageFrom(source ProjectCreateServer, image api.Image, aliases []api.ImageAlias) (incus.RemoteOperation, error) {
	return s.inner.CopyImage(source.imageServer(), image, &incus.ImageCopyArgs{Aliases: aliases})
}
