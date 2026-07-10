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
)

type fakeTenantListServer struct {
	projects []api.Project
	err      error
}

func (s fakeTenantListServer) GetProjects() ([]api.Project, error) {
	return s.projects, s.err
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
