package incusx

import (
	"context"
	"fmt"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type TenantListServer interface {
	GetProjects() ([]api.Project, error)
}

type TenantStore struct {
	Remote     string
	ConfigPath string
	Server     TenantListServer
}

func NewTenantStore(remote string) TenantStore {
	return TenantStore{Remote: remote}
}

func NewTenantStoreForServer(server incus.InstanceServer) TenantStore {
	return TenantStore{Server: server}
}

func (s TenantStore) ListProjects(ctx context.Context) ([]tenant.IncusProject, error) {
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
