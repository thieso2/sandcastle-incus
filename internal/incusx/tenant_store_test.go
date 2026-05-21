package incusx

import (
	"context"
	"errors"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/meta"
)

type fakeTenantListServer struct {
	projects []api.Project
	err      error
}

func (s fakeTenantListServer) GetProjects() ([]api.Project, error) {
	return s.projects, s.err
}

func TestTenantStoreUsesInjectedServer(t *testing.T) {
	config, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}

	store := TenantStore{Server: fakeTenantListServer{projects: []api.Project{{
		Name: "sc-acme",
		ProjectPut: api.ProjectPut{
			Config: api.ConfigMap(config),
		},
	}}}}

	projects, err := store.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("len(projects) = %d, want 1", len(projects))
	}
	if projects[0].Name != "sc-acme" {
		t.Fatalf("project name = %q", projects[0].Name)
	}
	if projects[0].Config[meta.KeyTenant] != "acme" {
		t.Fatalf("tenant metadata = %q", projects[0].Config[meta.KeyTenant])
	}
}

func TestTenantStoreWrapsListErrors(t *testing.T) {
	store := TenantStore{Server: fakeTenantListServer{err: errors.New("boom")}}
	_, err := store.ListProjects(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}
