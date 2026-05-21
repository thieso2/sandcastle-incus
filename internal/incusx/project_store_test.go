package incusx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
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
	config, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}

	store := ProjectStore{Server: fakeProjectServer{projects: []api.Project{{
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

func TestProjectStoreMergesProjectNamespaceFile(t *testing.T) {
	config, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := json.Marshal(projectNamespaceState{Projects: []meta.Project{{Name: "default"}, {Name: "website"}}})
	if err != nil {
		t.Fatal(err)
	}
	store := ProjectStore{
		Server: fakeProjectServer{projects: []api.Project{{
			Name: "sc-acme",
			ProjectPut: api.ProjectPut{
				Config: api.ConfigMap(config),
			},
		}}},
		Metadata: fakeProjectMetadataServer{files: map[string]string{projectNamespaceMetadataFile: string(state)}},
	}

	projects, err := store.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tenant, err := meta.ParseTenantConfig(projects[0].Config)
	if err != nil {
		t.Fatal(err)
	}
	if len(tenant.Projects) != 2 || tenant.Projects[1].Name != "website" {
		t.Fatalf("projects = %#v", tenant.Projects)
	}
}

func TestProjectStoreIgnoresMissingProjectNamespaceFile(t *testing.T) {
	config, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	store := ProjectStore{
		Server: fakeProjectServer{projects: []api.Project{{
			Name: "sc-acme",
			ProjectPut: api.ProjectPut{
				Config: api.ConfigMap(config),
			},
		}}},
		Metadata: fakeProjectMetadataServer{err: os.ErrNotExist},
	}

	projects, err := store.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tenant, err := meta.ParseTenantConfig(projects[0].Config)
	if err != nil {
		t.Fatal(err)
	}
	if len(tenant.Projects) != 1 || tenant.Projects[0].Name != "default" {
		t.Fatalf("projects = %#v", tenant.Projects)
	}
}

func TestProjectStoreMergesSSHKeyMetadataFile(t *testing.T) {
	config, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	store := ProjectStore{
		Server: fakeProjectServer{projects: []api.Project{{
			Name: "sc-acme",
			ProjectPut: api.ProjectPut{
				Config: api.ConfigMap(config),
			},
		}}},
		Metadata: fakeProjectMetadataServer{files: map[string]string{projectSSHKeyMetadataFile: "ssh-ed25519 test\n"}},
	}

	projects, err := store.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tenant, err := meta.ParseTenantConfig(projects[0].Config)
	if err != nil {
		t.Fatal(err)
	}
	if tenant.SSHPublicKey != "ssh-ed25519 test" {
		t.Fatalf("SSHPublicKey = %q", tenant.SSHPublicKey)
	}
}

func TestProjectStoreWrapsListErrors(t *testing.T) {
	store := ProjectStore{Server: fakeProjectServer{err: errors.New("boom")}}
	_, err := store.ListProjects(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

type fakeProjectMetadataServer struct {
	files map[string]string
	err   error
}

func (s fakeProjectMetadataServer) UseProject(name string) ProjectMetadataResourceServer {
	return fakeProjectMetadataResource{files: s.files, err: s.err}
}

type fakeProjectMetadataResource struct {
	files map[string]string
	err   error
}

func (r fakeProjectMetadataResource) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	if r.err != nil {
		return nil, nil, r.err
	}
	content, ok := r.files[filePath]
	if !ok {
		return nil, nil, os.ErrNotExist
	}
	return io.NopCloser(strings.NewReader(content)), &incus.InstanceFileResponse{}, nil
}
