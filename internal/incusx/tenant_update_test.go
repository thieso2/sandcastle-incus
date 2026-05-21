package incusx

import (
	"context"
	"io"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/thieso2/sandcastle-incus/internal/meta"
)

func TestSetTenantProjectsWritesNamespaceFile(t *testing.T) {
	resource := &fakeTenantMetadataUpdateResource{}
	server := &fakeTenantMetadataUpdateServer{resource: resource}
	manager := TenantSSHKeyManager{Server: server}

	err := manager.SetTenantProjects(context.Background(), "sc-acme", []meta.Project{{Name: "default"}, {Name: "website"}})
	if err != nil {
		t.Fatal(err)
	}
	if server.usedProject != "sc-acme" {
		t.Fatalf("usedProject = %q", server.usedProject)
	}
	if !resource.createdDir {
		t.Fatal("metadata directory was not created")
	}
	if resource.filePath != tenantProjectsFile {
		t.Fatalf("filePath = %q", resource.filePath)
	}
	if !strings.Contains(resource.content, `"name": "website"`) {
		t.Fatalf("content = %s", resource.content)
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
	usedProject string
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
