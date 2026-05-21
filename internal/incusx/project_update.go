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
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type TenantSSHKeyManager struct {
	Remote     string
	ConfigPath string
	Server     ProjectNamespaceUpdateServer
}

func NewProjectSSHKeyManager(remote string) TenantSSHKeyManager {
	return TenantSSHKeyManager{Remote: remote}
}

type ProjectNamespaceUpdateServer interface {
	UseProject(name string) ProjectNamespaceUpdateResourceServer
}

type ProjectNamespaceUpdateResourceServer interface {
	CreateStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error
}

func (m TenantSSHKeyManager) SetTenantSSHKey(_ context.Context, incusProjectName string, sshKey string) error {
	return m.writeProjectMetadataFile(incusProjectName, projectSSHKeyMetadataFile, strings.TrimSpace(sshKey)+"\n", "write project SSH key metadata")
}

func (m TenantSSHKeyManager) SetTenantProjects(_ context.Context, incusProjectName string, projects []meta.Project) error {
	state := projectNamespaceState{Projects: append([]meta.Project{}, projects...)}
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return m.writeProjectMetadataFile(incusProjectName, projectNamespaceMetadataFile, string(content), "write project namespace metadata")
}

func (m TenantSSHKeyManager) writeProjectMetadataFile(incusProjectName string, filePath string, content string, action string) error {
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
		server = sdkProjectNamespaceUpdateServer{inner: loadedServer}
	}
	projectServer := server.UseProject(incusProjectName)
	if err := projectServer.CreateStorageVolumeFile(incusProjectName, "custom", project.WorkspaceVolumeName, projectNamespaceMetadataDir, incus.InstanceFileArgs{
		Type: "directory",
		Mode: 0o755,
	}); err != nil && !api.StatusErrorCheck(err, 409) {
		return fmt.Errorf("create project metadata directory: %w", err)
	}
	if err := projectServer.CreateStorageVolumeFile(incusProjectName, "custom", project.WorkspaceVolumeName, filePath, incus.InstanceFileArgs{
		Content:   strings.NewReader(content),
		Type:      "file",
		Mode:      0o644,
		WriteMode: "overwrite",
	}); err != nil {
		return fmt.Errorf("%s for %s: %w", action, incusProjectName, err)
	}
	return nil
}

type sdkProjectNamespaceUpdateServer struct {
	inner incus.InstanceServer
}

func (s sdkProjectNamespaceUpdateServer) UseProject(name string) ProjectNamespaceUpdateResourceServer {
	return sdkProjectNamespaceUpdateResourceServer{inner: s.inner.UseProject(name), projectName: name}
}

type sdkProjectNamespaceUpdateResourceServer struct {
	inner       incus.InstanceServer
	projectName string
}

func (s sdkProjectNamespaceUpdateResourceServer) CreateStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error {
	return createStorageVolumeFile(s.inner, s.projectName, pool, volumeType, volumeName, filePath, args)
}
