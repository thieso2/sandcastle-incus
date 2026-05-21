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
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type ProjectServer interface {
	GetProjects() ([]api.Project, error)
}

type ProjectMetadataServer interface {
	UseProject(name string) ProjectMetadataResourceServer
}

type ProjectMetadataResourceServer interface {
	GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error)
}

type ProjectStore struct {
	Remote     string
	ConfigPath string
	Server     ProjectServer
	Metadata   ProjectMetadataServer
}

func NewProjectStore(remote string) ProjectStore {
	return ProjectStore{Remote: remote}
}

func NewProjectStoreForServer(server incus.InstanceServer) ProjectStore {
	return ProjectStore{Server: server, Metadata: sdkProjectMetadataServer{inner: server}}
}

func (s ProjectStore) ListProjects(ctx context.Context) ([]project.IncusProject, error) {
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
		metadata = sdkProjectMetadataServer{inner: loadedServer}
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
		config, err := tenantConfigWithProjectMetadataFiles(metadata.UseProject(output[i].Name), output[i].Name, output[i].Config)
		if err != nil {
			return nil, err
		}
		output[i].Config = config
	}
	return output, nil
}

func FromAPIProjects(projects []api.Project) []project.IncusProject {
	output := make([]project.IncusProject, 0, len(projects))
	for _, incusProject := range projects {
		output = append(output, project.IncusProject{
			Name:   incusProject.Name,
			Config: map[string]string(incusProject.Config),
		})
	}
	return output
}

type projectNamespaceState struct {
	Projects []meta.Project `json:"projects"`
}

const (
	projectNamespaceMetadataDir  = "/.sandcastle"
	projectNamespaceMetadataFile = projectNamespaceMetadataDir + "/projects.json"
	projectSSHKeyMetadataFile    = projectNamespaceMetadataDir + "/ssh_public_key"
)

func tenantConfigWithProjectMetadataFiles(server ProjectMetadataResourceServer, incusProjectName string, config map[string]string) (map[string]string, error) {
	if !meta.IsManaged(config) || config[meta.KeyKind] != meta.KindTenant {
		return config, nil
	}
	managed, err := meta.ParseTenantConfig(config)
	if err != nil {
		return nil, fmt.Errorf("parse tenant metadata for %s: %w", incusProjectName, err)
	}
	if state, ok, err := readProjectNamespaceState(server, incusProjectName); err != nil {
		return nil, err
	} else if ok {
		managed.Projects = append([]meta.Project{}, state.Projects...)
	}
	if sshKey, ok, err := readProjectSSHKey(server, incusProjectName); err != nil {
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

func readProjectNamespaceState(server ProjectMetadataResourceServer, incusProjectName string) (projectNamespaceState, bool, error) {
	content, _, err := server.GetStorageVolumeFile(incusProjectName, "custom", project.WorkspaceVolumeName, projectNamespaceMetadataFile)
	if isMissingProjectNamespaceMetadata(err) {
		return projectNamespaceState{}, false, nil
	}
	if err != nil {
		return projectNamespaceState{}, false, fmt.Errorf("read project namespace metadata for %s: %w", incusProjectName, err)
	}
	defer content.Close()
	data, err := io.ReadAll(content)
	if err != nil {
		return projectNamespaceState{}, false, fmt.Errorf("read project namespace metadata for %s: %w", incusProjectName, err)
	}
	var state projectNamespaceState
	if err := json.Unmarshal(data, &state); err != nil {
		return projectNamespaceState{}, false, fmt.Errorf("parse project namespace metadata for %s: %w", incusProjectName, err)
	}
	return state, true, nil
}

func readProjectSSHKey(server ProjectMetadataResourceServer, incusProjectName string) (string, bool, error) {
	content, _, err := server.GetStorageVolumeFile(incusProjectName, "custom", project.WorkspaceVolumeName, projectSSHKeyMetadataFile)
	if isMissingProjectNamespaceMetadata(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read project SSH key metadata for %s: %w", incusProjectName, err)
	}
	defer content.Close()
	data, err := io.ReadAll(content)
	if err != nil {
		return "", false, fmt.Errorf("read project SSH key metadata for %s: %w", incusProjectName, err)
	}
	return strings.TrimSpace(string(data)), true, nil
}

func isMissingProjectNamespaceMetadata(err error) bool {
	return api.StatusErrorCheck(err, http.StatusNotFound) || errors.Is(err, os.ErrNotExist)
}

type sdkProjectMetadataServer struct {
	inner incus.InstanceServer
}

func (s sdkProjectMetadataServer) UseProject(name string) ProjectMetadataResourceServer {
	return sdkProjectMetadataResourceServer{inner: s.inner.UseProject(name), projectName: name}
}

type sdkProjectMetadataResourceServer struct {
	inner       incus.InstanceServer
	projectName string
}

func (s sdkProjectMetadataResourceServer) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	return getStorageVolumeFile(s.inner, s.projectName, pool, volumeType, volumeName, filePath)
}
