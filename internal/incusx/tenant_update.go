package incusx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type TenantSSHKeyManager struct {
	Remote     string
	ConfigPath string
	Server     TenantMetadataUpdateServer
}

func NewTenantSSHKeyManager(remote string) TenantSSHKeyManager {
	return TenantSSHKeyManager{Remote: remote}
}

type TenantMetadataUpdateServer interface {
	UseProject(name string) TenantMetadataUpdateResourceServer
}

type TenantMetadataUpdateResourceServer interface {
	CreateStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error
}

func (m TenantSSHKeyManager) SetTenantSSHKey(_ context.Context, incusProjectName string, sshKey string) error {
	return m.writeTenantMetadataFile(incusProjectName, tenantSSHPublicKeyFile, strings.TrimSpace(sshKey)+"\n", "write tenant SSH key metadata")
}

func (m TenantSSHKeyManager) SetTenantProjects(_ context.Context, incusProjectName string, projects []meta.Project) error {
	state := tenantProjectNamespaceState{Projects: append([]meta.Project{}, projects...)}
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return m.writeTenantMetadataFile(incusProjectName, tenantProjectsFile, string(content), "write tenant project metadata")
}

func (m TenantSSHKeyManager) writeTenantMetadataFile(incusProjectName string, filePath string, content string, action string) error {
	server := m.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(m.ConfigPath)
		if err != nil {
			return fmt.Errorf("load Incus config: %w", err)
		}
		remote := m.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		loadedServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkTenantMetadataUpdateServer{inner: loadedServer}
	}
	tenantServer := server.UseProject(incusProjectName)
	if err := tenantServer.CreateStorageVolumeFile(incusProjectName, "custom", tenant.WorkspaceVolumeName, tenantMetadataDir, incus.InstanceFileArgs{
		Type: "directory",
		Mode: 0o755,
	}); err != nil && !api.StatusErrorCheck(err, 409) {
		return fmt.Errorf("create tenant metadata directory: %w", err)
	}
	if err := tenantServer.CreateStorageVolumeFile(incusProjectName, "custom", tenant.WorkspaceVolumeName, filePath, incus.InstanceFileArgs{
		Content:   strings.NewReader(content),
		Type:      "file",
		Mode:      0o644,
		WriteMode: "overwrite",
	}); err != nil {
		return fmt.Errorf("%s for %s: %w", action, incusProjectName, err)
	}
	return nil
}

type sdkTenantMetadataUpdateServer struct {
	inner incus.InstanceServer
}

func (s sdkTenantMetadataUpdateServer) UseProject(name string) TenantMetadataUpdateResourceServer {
	return sdkTenantMetadataUpdateResourceServer{inner: s.inner.UseProject(name), projectName: name}
}

type sdkTenantMetadataUpdateResourceServer struct {
	inner       incus.InstanceServer
	projectName string
}

func (s sdkTenantMetadataUpdateResourceServer) CreateStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error {
	return createStorageVolumeFile(s.inner, s.projectName, pool, volumeType, volumeName, filePath, args)
}
