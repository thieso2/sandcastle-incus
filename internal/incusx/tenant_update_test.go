package incusx

import (
	"context"
	"io"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/meta"
)

func TestSetTenantProjectsUpdatesTenantConfig(t *testing.T) {
	config, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		PrivateCIDR: "10.248.0.0/24",
		Projects:    []meta.Project{{Name: "default"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeTenantMetadataUpdateServer{
		project: &api.Project{
			Name: "sc-acme",
			ProjectPut: api.ProjectPut{
				Config: api.ConfigMap(config),
			},
		},
		etag: "test-etag",
	}
	manager := TenantSSHKeyManager{Server: server}

	err = manager.SetTenantProjects(context.Background(), "sc-acme", []meta.Project{{Name: "default"}, {Name: "website"}})
	if err != nil {
		t.Fatal(err)
	}
	if server.usedProject != "sc-acme" {
		t.Fatalf("usedProject = %q", server.usedProject)
	}
	if server.updateETag != "test-etag" {
		t.Fatalf("updateETag = %q", server.updateETag)
	}
	if server.updated == nil {
		t.Fatal("project was not updated")
	}
	updated, err := meta.ParseTenantConfig(map[string]string(server.updated.Config))
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Projects) != 2 || updated.Projects[1].Name != "website" {
		t.Fatalf("projects = %#v", updated.Projects)
	}
}

func TestSetTenantSSHKeyWritesMetadataFile(t *testing.T) {
	resource := &fakeTenantMetadataUpdateResource{}
	server := &fakeTenantMetadataUpdateServer{resource: resource}
	manager := TenantSSHKeyManager{Server: server}

	err := manager.SetTenantSSHKey(context.Background(), "sc-acme", "ssh-ed25519 test")
	if err != nil {
		t.Fatal(err)
	}
	if server.usedProject != "sc-acme" {
		t.Fatalf("usedProject = %q", server.usedProject)
	}
	if !resource.createdDir {
		t.Fatal("metadata directory was not created")
	}
	if resource.filePath != tenantSSHPublicKeyFile {
		t.Fatalf("filePath = %q", resource.filePath)
	}
	if resource.content != "ssh-ed25519 test\n" {
		t.Fatalf("content = %q", resource.content)
	}
}

type fakeTenantMetadataUpdateServer struct {
	resource    *fakeTenantMetadataUpdateResource
	project     *api.Project
	updated     *api.ProjectPut
	etag        string
	updateETag  string
	usedProject string
}

func (s *fakeTenantMetadataUpdateServer) GetProject(name string) (*api.Project, string, error) {
	s.usedProject = name
	return s.project, s.etag, nil
}

func (s *fakeTenantMetadataUpdateServer) UpdateProject(name string, project api.ProjectPut, ETag string) error {
	s.usedProject = name
	s.updated = &project
	s.updateETag = ETag
	return nil
}

func (s *fakeTenantMetadataUpdateServer) UseProject(name string) TenantMetadataUpdateResourceServer {
	s.usedProject = name
	return s.resource
}

type fakeTenantMetadataUpdateResource struct {
	createdDir bool
	filePath   string
	content    string
}

func (r *fakeTenantMetadataUpdateResource) CreateStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error {
	if args.Type == "directory" {
		r.createdDir = true
		return nil
	}
	content, err := io.ReadAll(args.Content)
	if err != nil {
		return err
	}
	r.filePath = filePath
	r.content = string(content)
	return nil
}
