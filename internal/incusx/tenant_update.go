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

func NewTenantSSHKeyManagerForServer(server incus.InstanceServer) TenantSSHKeyManager {
	return TenantSSHKeyManager{Server: sdkTenantMetadataUpdateServer{inner: server}}
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
	data, err := json.Marshal(projects)
	if err != nil {
		return fmt.Errorf("encode projects for %s: %w", incusProjectName, err)
	}
	return m.writeTenantMetadataFile(incusProjectName, tenantProjectsFile, string(data), "write tenant projects metadata")
}

func (m TenantSSHKeyManager) SetTenantUnixUser(_ context.Context, incusProjectName string, unixUser string) error {
	return m.writeTenantMetadataFile(incusProjectName, tenantUnixUserFile, strings.TrimSpace(unixUser)+"\n", "write tenant Unix user metadata")
}

func (m TenantSSHKeyManager) writeTenantMetadataFile(incusProjectName string, filePath string, content string, action string) error {
	server, err := m.server()
	if err != nil {
		return err
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

func (m TenantSSHKeyManager) server() (TenantMetadataUpdateServer, error) {
	server := m.Server
	if server != nil {
		return server, nil
	}
	loaded, err := cliconfig.LoadConfig(m.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load Incus config: %w", err)
	}
	remote := m.Remote
	if remote == "" {
		remote = loaded.DefaultRemote
	}
	loadedServer, err := loaded.GetInstanceServer(remote)
	if err != nil {
		return nil, fmt.Errorf("connect to Incus remote %q: %w", remote, err)
	}
	return sdkTenantMetadataUpdateServer{inner: loadedServer}, nil
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
