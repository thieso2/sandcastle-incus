package incusx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type TenantListServer interface {
	GetProjects() ([]api.Project, error)
}

type TenantMetadataServer interface {
	UseProject(name string) TenantMetadataResourceServer
}

type TenantMetadataResourceServer interface {
	GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error)
}

type TenantStore struct {
	Remote     string
	ConfigPath string
	Server     TenantListServer
	Metadata   TenantMetadataServer
}

func NewTenantStore(remote string) TenantStore {
	return TenantStore{Remote: remote}
}

func NewTenantStoreForServer(server incus.InstanceServer) TenantStore {
	return TenantStore{Server: server, Metadata: sdkTenantMetadataServer{inner: server}}
}

func (s TenantStore) ListProjects(ctx context.Context) ([]tenant.IncusProject, error) {
	server := s.Server
	metadata := s.Metadata
	if server == nil {
		loaded, err := cliconfig.LoadConfig(s.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("load Incus config: %w", err)
		}
		remote := s.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		loadedServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return nil, fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = loadedServer
		metadata = sdkTenantMetadataServer{inner: loadedServer}
	}

	projects, err := server.GetProjects()
	if err != nil {
		return nil, fmt.Errorf("list Incus projects: %w", err)
	}
	output := FromAPIProjects(projects)
	if metadata == nil {
		return output, nil
	}
	for i := range output {
		config, err := tenantConfigWithMetadataFiles(metadata.UseProject(output[i].Name), output[i].Name, output[i].Config)
		if err != nil {
			return nil, err
		}
		output[i].Config = config
	}
	return output, nil
}

func FromAPIProjects(projects []api.Project) []tenant.IncusProject {
	output := make([]tenant.IncusProject, 0, len(projects))
	for _, incusProject := range projects {
		output = append(output, tenant.IncusProject{
			Name:   incusProject.Name,
			Config: map[string]string(incusProject.Config),
		})
	}
	return output
}

type tenantProjectNamespaceState struct {
	Projects []meta.Project `json:"projects"`
}

const (
	tenantMetadataDir      = "/.sandcastle"
	tenantProjectsFile     = tenantMetadataDir + "/projects.json"
	tenantSSHPublicKeyFile = tenantMetadataDir + "/ssh_public_key"
)

func tenantConfigWithMetadataFiles(server TenantMetadataResourceServer, incusProjectName string, config map[string]string) (map[string]string, error) {
	if !meta.IsManaged(config) || config[meta.KeyKind] != meta.KindTenant {
		return config, nil
	}
	managed, err := meta.ParseTenantConfig(config)
	if err != nil {
		return nil, fmt.Errorf("parse tenant metadata for %s: %w", incusProjectName, err)
	}
	if state, ok, err := readTenantProjectNamespaceState(server, incusProjectName); err != nil {
		return nil, err
	} else if ok {
		managed.Projects = append([]meta.Project{}, state.Projects...)
	}
	if sshKey, ok, err := readTenantSSHKey(server, incusProjectName); err != nil {
		return nil, err
	} else if ok {
		managed.SSHPublicKey = sshKey
	}
	updated, err := meta.TenantConfig(managed)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func readTenantProjectNamespaceState(server TenantMetadataResourceServer, incusProjectName string) (tenantProjectNamespaceState, bool, error) {
	content, _, err := server.GetStorageVolumeFile(incusProjectName, "custom", tenant.WorkspaceVolumeName, tenantProjectsFile)
	if isMissingTenantMetadata(err) {
		return tenantProjectNamespaceState{}, false, nil
	}
	if err != nil {
		return tenantProjectNamespaceState{}, false, fmt.Errorf("read tenant project metadata for %s: %w", incusProjectName, err)
	}
	defer content.Close()
	data, err := io.ReadAll(content)
	if err != nil {
		return tenantProjectNamespaceState{}, false, fmt.Errorf("read tenant project metadata for %s: %w", incusProjectName, err)
	}
	var state tenantProjectNamespaceState
	if err := json.Unmarshal(data, &state); err != nil {
		return tenantProjectNamespaceState{}, false, fmt.Errorf("parse tenant project metadata for %s: %w", incusProjectName, err)
	}
	return state, true, nil
}

func readTenantSSHKey(server TenantMetadataResourceServer, incusProjectName string) (string, bool, error) {
	content, _, err := server.GetStorageVolumeFile(incusProjectName, "custom", tenant.WorkspaceVolumeName, tenantSSHPublicKeyFile)
	if isMissingTenantMetadata(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read tenant SSH key metadata for %s: %w", incusProjectName, err)
	}
	defer content.Close()
	data, err := io.ReadAll(content)
	if err != nil {
		return "", false, fmt.Errorf("read tenant SSH key metadata for %s: %w", incusProjectName, err)
	}
	return strings.TrimSpace(string(data)), true, nil
}

func isMissingTenantMetadata(err error) bool {
	return api.StatusErrorCheck(err, http.StatusNotFound) ||
		errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, os.ErrPermission)
}

type sdkTenantMetadataServer struct {
	inner incus.InstanceServer
}

func (s sdkTenantMetadataServer) UseProject(name string) TenantMetadataResourceServer {
	return sdkTenantMetadataResourceServer{inner: s.inner.UseProject(name), projectName: name}
}

type sdkTenantMetadataResourceServer struct {
	inner       incus.InstanceServer
	projectName string
}

func (s sdkTenantMetadataResourceServer) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	return getStorageVolumeFile(s.inner, s.projectName, pool, volumeType, volumeName, filePath)
}
