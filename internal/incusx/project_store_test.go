package incusx

import (
	"context"
	"errors"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/meta"
)

type fakeProjectServer struct {
	projects []api.Project
	err      error
}

func (s fakeProjectServer) GetProjects() ([]api.Project, error) {
	return s.projects, s.err
}

func TestProjectStoreUsesInjectedServer(t *testing.T) {
	config, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}

	store := ProjectStore{Server: fakeProjectServer{projects: []api.Project{{
		Name: "sc-alice-myproject",
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
	if projects[0].Name != "sc-alice-myproject" {
		t.Fatalf("project name = %q", projects[0].Name)
	}
	if projects[0].Config[meta.KeyOwner] != "alice" {
		t.Fatalf("project owner metadata = %q", projects[0].Config[meta.KeyOwner])
	}
}

func TestProjectStoreWrapsListErrors(t *testing.T) {
	store := ProjectStore{Server: fakeProjectServer{err: errors.New("boom")}}
	_, err := store.ListProjects(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}
