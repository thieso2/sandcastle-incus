package incusx

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/share"
)

func TestSetTenantProjectsWritesMetadataFile(t *testing.T) {
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
	var projects []meta.Project
	if err := json.Unmarshal([]byte(resource.content), &projects); err != nil {
		t.Fatalf("parse projects content: %v", err)
	}
	if len(projects) != 2 || projects[1].Name != "website" {
		t.Fatalf("projects = %#v", projects)
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

func TestSourceDirectoryStatusAcceptsSafeTreeWithDotfiles(t *testing.T) {
	manager := TenantSSHKeyManager{Server: &fakeTenantMetadataUpdateServer{resource: &fakeTenantMetadataUpdateResource{files: map[string]fakeVolumeFile{
		"default/docs":                  {typ: "directory", entries: []string{".env", "nested"}},
		"default/docs/.env":             {typ: "file"},
		"default/docs/nested":           {typ: "directory", entries: []string{"readme.md", "link"}},
		"default/docs/nested/readme.md": {typ: "file"},
		"default/docs/nested/link":      {typ: "symlink", content: "../.env"},
	}}}}
	status, err := manager.SourceDirectoryStatus(context.Background(), "sc-acme", "default", "docs")
	if err != nil {
		t.Fatal(err)
	}
	if status != (share.SourceStatus{Exists: true, Safe: true}) {
		t.Fatalf("status = %#v", status)
	}
}

func TestSourceDirectoryStatusRejectsEscapingSymlink(t *testing.T) {
	manager := TenantSSHKeyManager{Server: &fakeTenantMetadataUpdateServer{resource: &fakeTenantMetadataUpdateResource{files: map[string]fakeVolumeFile{
		"default/docs":        {typ: "directory", entries: []string{"escape"}},
		"default/docs/escape": {typ: "symlink", content: "../../other"},
	}}}}
	status, err := manager.SourceDirectoryStatus(context.Background(), "sc-acme", "default", "docs")
	if err != nil {
		t.Fatal(err)
	}
	if status.Exists != true || status.Safe != false || !strings.Contains(status.Reason, "symlink") {
		t.Fatalf("status = %#v", status)
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
	files      map[string]fakeVolumeFile
}

func (r *fakeTenantMetadataUpdateResource) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	if r.files != nil {
		file, ok := r.files[filePath]
		if !ok {
			return nil, nil, os.ErrNotExist
		}
		return io.NopCloser(strings.NewReader(file.content)), &incus.InstanceFileResponse{
			Type:    file.typ,
			Entries: append([]string{}, file.entries...),
		}, nil
	}
	return nil, nil, os.ErrNotExist
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

type fakeVolumeFile struct {
	typ     string
	entries []string
	content string
}
