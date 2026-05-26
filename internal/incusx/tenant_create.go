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
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type TenantCreateServer interface {
	GetProject(name string) (*api.Project, string, error)
	CreateProject(project api.ProjectsPost) error
	UpdateProject(name string, project api.ProjectPut, ETag string) error
	UseProject(name string) TenantResourceServer
	GetStoragePool(name string) (*api.StoragePool, string, error)
	CreateStoragePool(pool api.StoragePoolsPost) error
	GetImage(ref string) (*api.Image, string, error)
	GetImageAlias(name string) (*api.ImageAliasesEntry, string, error)
	imageServer() incus.ImageServer
}

type TenantResourceServer interface {
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
	UpdateInstance(name string, instance api.InstancePut, etag string) (incus.Operation, error)
	UpdateInstanceState(name string, state api.InstanceStatePut, ETag string) (incus.Operation, error)
	CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
	GetImage(ref string) (*api.Image, string, error)
	GetImageAlias(name string) (*api.ImageAliasesEntry, string, error)
	CreateImageAlias(alias api.ImageAliasesPost) error
	CopyImageFrom(source TenantCreateServer, image api.Image, aliases []api.ImageAlias) (incus.RemoteOperation, error)
}

type TenantCreator struct {
	Remote     string
	ConfigPath string
	Server     TenantCreateServer
	Log        func(string)
}

func NewTenantCreator(remote string) TenantCreator {
	return TenantCreator{Remote: remote}
}

func NewTenantCreatorForServer(server incus.InstanceServer) TenantCreator {
	return TenantCreator{Server: sdkTenantCreateServer{inner: server}}
}

func (c TenantCreator) WithVerbose(enabled bool, w io.Writer) TenantCreator {
	if enabled {
		c.Log = func(msg string) { fmt.Fprintln(w, "[tenant-create] "+msg) }
	}
	return c
}

func (c TenantCreator) log(msg string) {
	if c.Log != nil {
		c.Log(msg)
	}
}

func (c TenantCreator) CreateTenant(ctx context.Context, plan tenant.CreatePlan) error {
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
		server = sdkTenantCreateServer{inner: instanceServer}
	}

	c.log("ensure project " + plan.IncusProject)
	if err := ensureProject(server, plan); err != nil {
		return err
	}
	c.log("ensure infra project " + plan.InfraProject)
	if err := ensureInfraProject(server, plan); err != nil {
		return err
	}
	c.log("ensure native project " + plan.NativeProject)
	if err := ensureNativeProject(server, plan); err != nil {
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
	c.log("ensure tenant CA")
	if err := ensureTenantCA(projectServer, plan); err != nil {
		return err
	}
	c.log("ensure tenant profiles")
	if err := ensureTenantProfiles(projectServer, plan); err != nil {
		return err
	}
	infraServer := server.UseProject(plan.InfraProject)
	c.log("ensure infra project images")
	if err := ensureProjectImages(server, infraServer, plan.InfraImageAliases); err != nil {
		return err
	}
	for _, sidecar := range plan.Sidecars {
		c.log("ensure sidecar " + sidecar.Name + " (image: " + sidecar.ImageAlias + ")")
		if err := ensureSidecar(infraServer, sidecar); err != nil {
			return err
		}
		c.log("configure network for sidecar " + sidecar.Name)
		if err := configureSidecarNetwork(infraServer, sidecar, plan.PrivateCIDR); err != nil {
			return err
		}
	}
	c.log("ensure DNS files")
	if err := ensureDNSFiles(infraServer, plan); err != nil {
		return err
	}
	c.log("restart CoreDNS")
	if err := restartCoreDNS(infraServer); err != nil {
		return err
	}
	c.log("done")
	return nil
}

func ensureProject(server TenantCreateServer, plan tenant.CreatePlan) error {
	existing, etag, err := server.GetProject(plan.IncusProject)
	if err != nil {
		// 404 = project doesn't exist.
		// 403 = Incus fine-grained auth: the calling cert hasn't been granted access
		//       to this project yet (which also means it doesn't exist from our perspective).
		// In both cases, attempt to create it.
		if api.StatusErrorCheck(err, http.StatusNotFound) || api.StatusErrorCheck(err, http.StatusForbidden) {
			cfg := mergeConfig(isolatedProjectFeatureConfig(), plan.TenantMetadataConfig)
			return server.CreateProject(api.ProjectsPost{
				Name: plan.IncusProject,
				ProjectPut: api.ProjectPut{
					Description: "Sandcastle tenant " + plan.Reference,
					Config:      api.ConfigMap(cfg),
				},
			})
		}
		return fmt.Errorf("get Incus project %s: %w", plan.IncusProject, err)
	}
	config := mergeConfig(map[string]string(existing.Config), plan.TenantMetadataConfig)
	if err := server.UpdateProject(plan.IncusProject, api.ProjectPut{
		Description: existing.Description,
		Config:      api.ConfigMap(config),
	}, etag); err != nil {
		return fmt.Errorf("update Incus project %s metadata: %w", plan.IncusProject, err)
	}
	return nil
}

func ensureInfraProject(server TenantCreateServer, plan tenant.CreatePlan) error {
	_, _, err := server.GetProject(plan.InfraProject)
	if err == nil {
		return nil
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) && !api.StatusErrorCheck(err, http.StatusForbidden) {
		return fmt.Errorf("get Incus project %s: %w", plan.InfraProject, err)
	}
	return server.CreateProject(api.ProjectsPost{
		Name: plan.InfraProject,
		ProjectPut: api.ProjectPut{
			Description: "Sandcastle sidecar infrastructure for " + plan.Reference,
			Config: api.ConfigMap{
				"features.images": "true",
				meta.KeyKind:      "infra",
				meta.KeyTenant:    plan.Reference,
				meta.KeyVersion:   "1",
			},
		},
	})
}

func ensureNativeProject(server TenantCreateServer, plan tenant.CreatePlan) error {
	_, _, err := server.GetProject(plan.NativeProject)
	if err == nil {
		return nil
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) && !api.StatusErrorCheck(err, http.StatusForbidden) {
		return fmt.Errorf("get Incus project %s: %w", plan.NativeProject, err)
	}
	return server.CreateProject(api.ProjectsPost{
		Name: plan.NativeProject,
		ProjectPut: api.ProjectPut{
			Description: "Sandcastle native Incus workspace for " + plan.Reference,
			Config: api.ConfigMap{
				meta.KeyKind:    "native",
				meta.KeyTenant:  plan.Reference,
				meta.KeyVersion: "1",
			},
		},
	})
}

func isolatedProjectFeatureConfig() map[string]string {
	return map[string]string{
		"features.images":          "true",
		"features.profiles":        "true",
		"features.storage.buckets": "true",
		"features.storage.volumes": "true",
	}
}

func ensureProjectImages(source TenantCreateServer, target TenantResourceServer, aliases []string) error {
	for _, aliasName := range aliases {
		if err := ensureProjectImage(source, target, aliasName); err != nil {
			return err
		}
	}
	return nil
}

func ensureProjectImage(source TenantCreateServer, target TenantResourceServer, aliasName string) error {
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

func ensurePrivateNetwork(server TenantResourceServer, plan tenant.CreatePlan) error {
	_, _, err := server.GetNetwork(plan.PrivateNetwork)
	if err == nil {
		return nil
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get private network %s: %w", plan.PrivateNetwork, err)
	}
	return server.CreateNetwork(networkRequest(plan))
}

func ensureStorageVolume(server TenantResourceServer, pool string, volume api.StorageVolumesPost) error {
	_, _, err := server.GetStoragePoolVolume(pool, volume.Type, volume.Name)
	if err == nil {
		return nil
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get storage volume %s/%s: %w", pool, volume.Name, err)
	}
	return server.CreateStoragePoolVolume(pool, volume)
}

func ensureTenantCA(server TenantResourceServer, plan tenant.CreatePlan) error {
	if len(plan.TenantCA.CertificatePEM) == 0 || len(plan.TenantCA.PrivateKeyPEM) == 0 {
		return fmt.Errorf("tenant CA material is missing")
	}
	if err := server.CreateStorageVolumeFile(plan.StoragePool, "custom", plan.CAVolume, plan.TenantCA.CertificatePath, incus.InstanceFileArgs{
		Content:   strings.NewReader(string(plan.TenantCA.CertificatePEM)),
		Type:      "file",
		Mode:      0o644,
		WriteMode: "overwrite",
	}); err != nil {
		return fmt.Errorf("write tenant CA certificate: %w", err)
	}
	if err := server.CreateStorageVolumeFile(plan.StoragePool, "custom", plan.CAVolume, plan.TenantCA.PrivateKeyPath, incus.InstanceFileArgs{
		Content:   strings.NewReader(string(plan.TenantCA.PrivateKeyPEM)),
		Type:      "file",
		Mode:      0o600,
		WriteMode: "overwrite",
	}); err != nil {
		return fmt.Errorf("write tenant CA private key: %w", err)
	}
	return nil
}

func ensureSidecar(server TenantResourceServer, sidecar tenant.SidecarPlan) error {
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

func ensureDNSFiles(server TenantResourceServer, plan tenant.CreatePlan) error {
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
	op, err := server.ExecInstance(tenant.DNSName, api.InstanceExecPost{
		Command: []string{"/bin/sh", "-c", strings.Join([]string{
			"pkill -x coredns >/dev/null 2>&1 || true",
			"systemctl stop systemd-resolved.service 2>/dev/null || true",
			"systemctl mask systemd-resolved.service 2>/dev/null || true",
			"printf '%s\n' '[Unit]' 'Description=CoreDNS tenant resolver' 'After=network-online.target sandcastle-sidecar-network.service' 'Wants=network-online.target' '' '[Service]' 'ExecStart=/usr/local/bin/coredns -conf /etc/coredns/Corefile' 'Restart=on-failure' '' '[Install]' 'WantedBy=multi-user.target' > /etc/systemd/system/coredns.service",
			"systemctl daemon-reload",
			"systemctl enable --now coredns.service",
			"systemctl restart coredns.service",
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
		return fmt.Errorf("restart CoreDNS in %s: %w", tenant.DNSName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for CoreDNS restart in %s (stderr: %s): %w", tenant.DNSName, stderr.String(), err)
	}
	<-dataDone
	return nil
}

func configureSidecarNetwork(server TenantResourceServer, sidecar tenant.SidecarPlan, privateCIDR string) error {
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
		"install -d -m 0755 /usr/local/sbin /etc/systemd/system",
		"printf '%s\n' '#!/bin/sh' 'set -eu' '/usr/sbin/ip link set eth0 up' '/usr/sbin/ip addr replace " + ipWithPrefix + " dev eth0' '/usr/sbin/ip route replace default via " + gateway + "' > /usr/local/sbin/sandcastle-sidecar-network",
		"chmod 0755 /usr/local/sbin/sandcastle-sidecar-network",
		"printf '%s\n' '[Unit]' 'Description=Sandcastle sidecar static network' 'After=network-pre.target' 'Before=network-online.target' '' '[Service]' 'Type=oneshot' 'ExecStart=/usr/local/sbin/sandcastle-sidecar-network' 'RemainAfterExit=yes' '' '[Install]' 'WantedBy=multi-user.target' > /etc/systemd/system/sandcastle-sidecar-network.service",
		"/usr/local/sbin/sandcastle-sidecar-network",
		"systemctl daemon-reload",
		"systemctl enable sandcastle-sidecar-network.service",
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

func ensureStoragePool(server TenantCreateServer, plan tenant.CreatePlan) error {
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
		meta.KeyTenant:  plan.Reference,
		meta.KeyVersion: "1",
	}
	if source := adminPool.Config["source"]; source != "" && adminPool.Driver != "dir" {
		poolConfig["source"] = source + "/" + plan.StoragePool
	}
	return server.CreateStoragePool(api.StoragePoolsPost{
		Name:   plan.StoragePool,
		Driver: adminPool.Driver,
		StoragePoolPut: api.StoragePoolPut{
			Description: "Sandcastle per-tenant storage for " + plan.Reference,
			Config:      poolConfig,
		},
	})
}

func ensureTenantProfiles(server TenantResourceServer, plan tenant.CreatePlan) error {
	profilePut := tenantContainerProfilePut(plan)
	if err := ensureExactProfile(server, "container", profilePut); err != nil {
		return err
	}
	return ensureDefaultProfile(server, profilePut)
}

func tenantContainerProfilePut(plan tenant.CreatePlan) api.ProfilePut {
	return api.ProfilePut{
		Description: "Sandcastle container defaults for " + plan.Reference,
		Config: api.ConfigMap{
			meta.KeyKind:    "profile",
			meta.KeyTenant:  plan.Reference,
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
}

func ensureExactProfile(server TenantResourceServer, name string, profilePut api.ProfilePut) error {
	_, etag, err := server.GetProfile(name)
	if err == nil {
		return server.UpdateProfile(name, profilePut, etag)
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get %s profile: %w", name, err)
	}
	return server.CreateProfile(api.ProfilesPost{
		Name:       name,
		ProfilePut: profilePut,
	})
}

func ensureDefaultProfile(server TenantResourceServer, desired api.ProfilePut) error {
	existing, etag, err := server.GetProfile("default")
	if err != nil {
		if !api.StatusErrorCheck(err, http.StatusNotFound) {
			return fmt.Errorf("get default profile: %w", err)
		}
		return server.CreateProfile(api.ProfilesPost{
			Name:       "default",
			ProfilePut: desired,
		})
	}
	merged := mergeDefaultProfile(*existing, desired)
	return server.UpdateProfile("default", merged, etag)
}

func mergeDefaultProfile(existing api.Profile, desired api.ProfilePut) api.ProfilePut {
	merged := api.ProfilePut{
		Description: existing.Description,
		Config:      copyConfigMap(existing.Config),
		Devices:     copyDevicesMap(existing.Devices),
	}
	if merged.Description == "" || strings.HasPrefix(merged.Description, "Default Incus profile") {
		merged.Description = desired.Description
	}
	for key, value := range desired.Config {
		if _, ok := merged.Config[key]; !ok {
			merged.Config[key] = value
		}
	}
	for name, device := range desired.Devices {
		merged.Devices[name] = copyDevice(device)
	}
	return merged
}

func copyConfigMap(input api.ConfigMap) api.ConfigMap {
	output := api.ConfigMap{}
	for key, value := range input {
		output[key] = value
	}
	return output
}

func copyDevicesMap(input api.DevicesMap) api.DevicesMap {
	output := api.DevicesMap{}
	for name, device := range input {
		output[name] = copyDevice(device)
	}
	return output
}

func copyDevice(input map[string]string) map[string]string {
	output := map[string]string{}
	for key, value := range input {
		output[key] = value
	}
	return output
}

func networkRequest(plan tenant.CreatePlan) api.NetworksPost {
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
				meta.KeyTenant:      plan.Reference,
				meta.KeyPrivateCIDR: plan.PrivateCIDR,
				meta.KeyVersion:     "1",
			},
		},
	}
}

func volumeRequests(plan tenant.CreatePlan) []api.StorageVolumesPost {
	return []api.StorageVolumesPost{
		volumeRequest(plan, plan.HomeVolume, "Sandcastle home state for "+plan.Reference),
		volumeRequest(plan, plan.WorkspaceVolume, "Sandcastle workspace state for "+plan.Reference),
		volumeRequest(plan, plan.CAVolume, "Sandcastle tenant CA state for "+plan.Reference),
	}
}

func volumeRequest(plan tenant.CreatePlan, name string, description string) api.StorageVolumesPost {
	return api.StorageVolumesPost{
		Name:        name,
		Type:        "custom",
		ContentType: "filesystem",
		StorageVolumePut: api.StorageVolumePut{
			Description: description,
			Config: api.ConfigMap{
				meta.KeyKind:    "volume",
				meta.KeyTenant:  plan.Reference,
				meta.KeyVersion: "1",
			},
		},
	}
}

func sidecarRequest(sidecar tenant.SidecarPlan) api.InstancesPost {
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

func devicesMap(devices map[string]tenant.Device) api.DevicesMap {
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

type sdkTenantCreateServer struct {
	inner incus.InstanceServer
}

func (s sdkTenantCreateServer) GetProject(name string) (*api.Project, string, error) {
	return s.inner.GetProject(name)
}

func (s sdkTenantCreateServer) CreateProject(project api.ProjectsPost) error {
	return s.inner.CreateProject(project)
}

func (s sdkTenantCreateServer) GetImage(ref string) (*api.Image, string, error) {
	return s.inner.GetImage(ref)
}

func (s sdkTenantCreateServer) GetImageAlias(name string) (*api.ImageAliasesEntry, string, error) {
	return s.inner.GetImageAlias(name)
}

func (s sdkTenantCreateServer) imageServer() incus.ImageServer {
	return s.inner
}

func (s sdkTenantCreateServer) UpdateProject(name string, project api.ProjectPut, etag string) error {
	return s.inner.UpdateProject(name, project, etag)
}

func (s sdkTenantCreateServer) UseProject(name string) TenantResourceServer {
	return sdkResourceServer{inner: s.inner.UseProject(name), projectName: name}
}

func (s sdkTenantCreateServer) GetStoragePool(name string) (*api.StoragePool, string, error) {
	return s.inner.GetStoragePool(name)
}

func (s sdkTenantCreateServer) CreateStoragePool(pool api.StoragePoolsPost) error {
	return s.inner.CreateStoragePool(pool)
}

type sdkResourceServer struct {
	inner       incus.InstanceServer
	projectName string
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
	return createStorageVolumeFile(s.inner, s.projectName, pool, volumeType, volumeName, filePath, args)
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

func (s sdkResourceServer) UpdateInstance(name string, instance api.InstancePut, etag string) (incus.Operation, error) {
	return s.inner.UpdateInstance(name, instance, etag)
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

func (s sdkResourceServer) CopyImageFrom(source TenantCreateServer, image api.Image, aliases []api.ImageAlias) (incus.RemoteOperation, error) {
	return s.inner.CopyImage(source.imageServer(), image, &incus.ImageCopyArgs{Aliases: aliases, Mode: "relay"})
}
