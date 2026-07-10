package machine

import (
	"context"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func tenantStoreForTest(t *testing.T) tenant.MemoryStore {
	t.Helper()
	return tenantStoreForTestWithTenants(t, "acme")
}

func tenantStoreForTestWithTenants(t *testing.T, names ...string) tenant.MemoryStore {
	t.Helper()
	projects := make([]tenant.IncusProject, 0, len(names))
	for _, name := range names {
		config, err := meta.TenantConfig(meta.Tenant{
			Tenant:      name,
			PrivateCIDR: "10.248.0.0/24",
			Projects: []meta.Project{
				{Name: "default"},
				{Name: "website"},
			},
			SSHPublicKey: "ssh-ed25519 test",
		})
		if err != nil {
			t.Fatal(err)
		}
		projects = append(projects, tenant.IncusProject{Name: "sc-" + name, Config: config})
	}
	return tenant.MemoryStore{Projects: projects}
}

type fakeMachineStore struct {
	machines  []meta.Machine
	unmanaged []UnmanagedMachine
}

type filteringTenantStoreForTest struct {
	inner       tenant.MemoryStore
	filters     []string
	filtersUsed *[][]string
}

func (s filteringTenantStoreForTest) WithTenantFilter(names ...string) tenant.IncusTenantStore {
	s.filters = append([]string{}, names...)
	return s
}

func (s filteringTenantStoreForTest) ListProjects(ctx context.Context) ([]tenant.IncusProject, error) {
	if s.filtersUsed != nil {
		*s.filtersUsed = append(*s.filtersUsed, append([]string{}, s.filters...))
	}
	projects, err := s.inner.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	if len(s.filters) == 0 {
		return projects, nil
	}
	wanted := map[string]struct{}{}
	for _, name := range s.filters {
		wanted[name] = struct{}{}
	}
	filtered := make([]tenant.IncusProject, 0, len(projects))
	for _, project := range projects {
		config, err := meta.ParseTenantConfig(project.Config)
		if err != nil {
			return nil, err
		}
		if _, ok := wanted[config.Tenant]; ok {
			filtered = append(filtered, project)
		}
	}
	return filtered, nil
}

func (s fakeMachineStore) ListMachines(ctx context.Context, summary tenant.Summary) ([]meta.Machine, error) {
	return s.machines, nil
}

func (s fakeMachineStore) ListUnmanagedMachines(ctx context.Context, summary tenant.Summary) ([]UnmanagedMachine, error) {
	return s.unmanaged, nil
}
