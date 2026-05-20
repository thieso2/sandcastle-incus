package incusx

import (
	"context"
	"fmt"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type ProjectServer interface {
	GetProjects() ([]api.Project, error)
}

type ProjectStore struct {
	Remote     string
	ConfigPath string
	Server     ProjectServer
}

func NewProjectStore(remote string) ProjectStore {
	return ProjectStore{Remote: remote}
}

func (s ProjectStore) ListProjects(ctx context.Context) ([]project.IncusProject, error) {
	server := s.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(s.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("load Incus config: %w", err)
		}
		remote := s.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		server, err = loaded.GetInstanceServer(remote)
		if err != nil {
			return nil, fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
	}

	projects, err := server.GetProjects()
	if err != nil {
		return nil, fmt.Errorf("list Incus projects: %w", err)
	}
	return FromAPIProjects(projects), nil
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
