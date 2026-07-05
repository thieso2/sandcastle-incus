package incusx

import (
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
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
	SupportsIdmappedMounts() bool
	imageServer() incus.ImageServer
}

type TenantResourceServer interface {
	GetNetwork(name string) (*api.Network, string, error)
	CreateNetwork(network api.NetworksPost) error
	GetStoragePoolVolume(pool string, volType string, name string) (*api.StorageVolume, string, error)
	CreateStoragePoolVolume(pool string, volume api.StorageVolumesPost) error
	GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error)
	CreateStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error
	GetProfile(name string) (*api.Profile, string, error)
	CreateProfile(profile api.ProfilesPost) error
	UpdateProfile(name string, profile api.ProfilePut, ETag string) error
	GetInstance(name string) (*api.Instance, string, error)
	GetInstanceState(name string) (*api.InstanceState, string, error)
	CreateInstance(instance api.InstancesPost) (incus.Operation, error)
	DeleteInstance(name string) (incus.Operation, error)
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

// EnsureAuxProjects creates the infra/native Incus projects and their sidecar instances for
// mainProjectName if missing. It is a recovery path for tenants in an incomplete state.

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
		"install -d -m 0755 /usr/local/sbin /etc/systemd/system /etc/systemd/system/multi-user.target.wants",
		"printf '%s\n' '#!/bin/sh' 'set -eu' '/usr/sbin/ip link set eth0 up' '/usr/sbin/ip addr replace " + ipWithPrefix + " dev eth0' '/usr/sbin/ip route replace default via " + gateway + "' > /usr/local/sbin/sandcastle-sidecar-network",
		"chmod 0755 /usr/local/sbin/sandcastle-sidecar-network",
		"printf '%s\n' '[Unit]' 'Description=Sandcastle sidecar static network' 'After=network-pre.target' 'Before=network-online.target' '' '[Service]' 'Type=oneshot' 'ExecStart=/usr/local/sbin/sandcastle-sidecar-network' 'RemainAfterExit=yes' '' '[Install]' 'WantedBy=multi-user.target' > /etc/systemd/system/sandcastle-sidecar-network.service",
		// Enable the unit by creating the wants-symlink directly. This runs right
		// after the sidecar starts, when systemd's D-Bus may not be up yet, so a
		// plain `systemctl enable` can fail and silently leave the unit disabled —
		// the immediate apply below still sets the IP live, but after a host reboot
		// nothing reapplies it and the sidecar (e.g. sc-dns) comes up with no eth0
		// address, breaking tenant DNS. The symlink does not need the bus.
		"ln -sf /etc/systemd/system/sandcastle-sidecar-network.service /etc/systemd/system/multi-user.target.wants/sandcastle-sidecar-network.service",
		"systemctl daemon-reload 2>/dev/null || true",
		"systemctl enable sandcastle-sidecar-network.service 2>/dev/null || true",
		"/usr/local/sbin/sandcastle-sidecar-network",
	}
	if sidecar.Role == "dns" {
		cmds = append(cmds,
			"pkill -x tailscaled >/dev/null 2>&1 || true",
			"systemctl disable --now tailscaled.service 2>/dev/null || true",
			"systemctl mask tailscaled.service 2>/dev/null || true",
		)
	}
	if sidecar.Role == "tailscale" {
		cmds = append(cmds,
			"systemctl unmask tailscaled.service 2>/dev/null || true",
			"systemctl disable --now tailscaled.service 2>/dev/null || true",
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

// SupportsIdmappedMounts reports whether the daemon's kernel offers idmapped
// mounts — required for security.shifted volumes. A container-hosted incus
// (nested CT) does not get the capability and shifted volume attachment fails
// with "idmapping abilities are required but aren't supported on system".
func (s sdkTenantCreateServer) SupportsIdmappedMounts() bool {
	server, _, err := s.inner.GetServer()
	if err != nil || server == nil {
		return false
	}
	return server.Environment.KernelFeatures["idmapped_mounts"] == "true"
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

func (s sdkResourceServer) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	return getStorageVolumeFile(s.inner, s.projectName, pool, volumeType, volumeName, filePath)
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

func (s sdkResourceServer) GetInstanceState(name string) (*api.InstanceState, string, error) {
	return s.inner.GetInstanceState(name)
}

func (s sdkResourceServer) CreateInstance(instance api.InstancesPost) (incus.Operation, error) {
	return s.inner.CreateInstance(instance)
}

func (s sdkResourceServer) DeleteInstance(name string) (incus.Operation, error) {
	return s.inner.DeleteInstance(name)
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
