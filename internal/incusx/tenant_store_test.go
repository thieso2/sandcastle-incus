package incusx

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
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

func TestTenantStoreDoesNotReadSSHKeyMetadataByDefault(t *testing.T) {
	config, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	store := TenantStore{
		Server: fakeTenantListServer{projects: []api.Project{{
			Name: "sc-acme",
			ProjectPut: api.ProjectPut{
				Config: api.ConfigMap(config),
			},
		}}},
		Metadata: fakeTenantMetadataServer{files: map[string]string{tenantSSHPublicKeyFile: "ssh-ed25519 test\n"}},
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
	if tenant.SSHPublicKey != "" {
		t.Fatalf("SSHPublicKey = %q", tenant.SSHPublicKey)
	}
}

func TestTenantStoreMergesSSHKeyMetadataFile(t *testing.T) {
	config, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	store := TenantStore{
		Server: fakeTenantListServer{projects: []api.Project{{
			Name: "sc-acme",
			ProjectPut: api.ProjectPut{
				Config: api.ConfigMap(config),
			},
		}}},
		Metadata:   fakeTenantMetadataServer{files: map[string]string{tenantSSHPublicKeyFile: "ssh-ed25519 test\n"}},
		LoadSSHKey: true,
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

func TestTenantStoreFilterSkipsUnrelatedTenantMetadataHydration(t *testing.T) {
	acmeConfig, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	someConfig, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "some",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.1.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	usedProjects := []string{}
	store := TenantStore{
		Server: fakeTenantListServer{projects: []api.Project{{
			Name: "sc-acme",
			ProjectPut: api.ProjectPut{
				Config: api.ConfigMap(acmeConfig),
			},
		}, {
			Name: "sc-some",
			ProjectPut: api.ProjectPut{
				Config: api.ConfigMap(someConfig),
			},
		}}},
		Metadata: fakeTenantMetadataServer{files: map[string]string{tenantUnixUserFile: "localuser\n"}, usedProjects: &usedProjects},
	}

	filtered := store.WithTenantFilter("some")
	projects, err := filtered.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Name != "sc-some" {
		t.Fatalf("projects = %#v", projects)
	}
	if len(usedProjects) != 1 || usedProjects[0] != "sc-some" {
		t.Fatalf("usedProjects = %#v, want only sc-some", usedProjects)
	}
}

func TestTenantStoreWrapsListErrors(t *testing.T) {
	store := TenantStore{Server: fakeTenantListServer{err: errors.New("boom")}}
	_, err := store.ListProjects(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

type fakeTenantMetadataServer struct {
	files        map[string]string
	err          error
	usedProjects *[]string
}

func (s fakeTenantMetadataServer) UseProject(name string) TenantMetadataResourceServer {
	if s.usedProjects != nil {
		*s.usedProjects = append(*s.usedProjects, name)
	}
	return fakeTenantMetadataResource{files: s.files, err: s.err}
}

type fakeTenantMetadataResource struct {
	files map[string]string
	err   error
}

func (r fakeTenantMetadataResource) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	if r.err != nil {
		return nil, nil, r.err
	}
	content, ok := r.files[filePath]
	if !ok {
		return nil, nil, os.ErrNotExist
	}
	return io.NopCloser(strings.NewReader(content)), &incus.InstanceFileResponse{}, nil
}
