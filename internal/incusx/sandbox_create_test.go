package incusx

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/certs"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
)

type fakeSandboxServer struct {
	resource *fakeSandboxResource
}

func (s fakeSandboxServer) UseProject(name string) SandboxResourceServer {
	return s.resource
}

type fakeSandboxResource struct {
	instance     *api.Instance
	created      *api.InstancesPost
	started      bool
	createdFiles map[string]string
	caFiles      map[string]string
}

func (r *fakeSandboxResource) GetInstance(name string) (*api.Instance, string, error) {
	if r.instance != nil {
		return r.instance, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (r *fakeSandboxResource) CreateInstance(instance api.InstancesPost) (incus.Operation, error) {
	r.created = &instance
	return fakeOperation{}, nil
}

func (r *fakeSandboxResource) UpdateInstanceState(name string, state api.InstanceStatePut, etag string) (incus.Operation, error) {
	if state.Action == "start" {
		r.started = true
	}
	return fakeOperation{}, nil
}

func (r *fakeSandboxResource) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
	if r.createdFiles == nil {
		r.createdFiles = map[string]string{}
	}
	if args.Content == nil {
		r.createdFiles[path] = args.Type
		return nil
	}
	content, err := io.ReadAll(args.Content)
	if err != nil {
		return err
	}
	r.createdFiles[path] = string(content)
	return nil
}

func (r *fakeSandboxResource) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	content, ok := r.caFiles[filePath]
	if !ok {
		return nil, nil, api.StatusErrorf(http.StatusNotFound, "not found")
	}
	return io.NopCloser(strings.NewReader(content)), &incus.InstanceFileResponse{Type: "file"}, nil
}

func TestSandboxCreatorCreatesInstance(t *testing.T) {
	plan := sandboxPlanForTest(t)
	resource := fakeSandboxResourceWithCA(t)
	creator := SandboxCreator{Server: fakeSandboxServer{resource: resource}}
	if err := creator.CreateSandbox(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if resource.created == nil {
		t.Fatal("expected instance creation")
	}
	if resource.created.Name != "sc-codex" {
		t.Fatalf("created name = %q", resource.created.Name)
	}
	if resource.created.Devices["eth0"]["ipv4.address"] != "10.248.0.20" {
		t.Fatalf("devices = %#v", resource.created.Devices)
	}
	if resource.createdFiles[sandbox.CaddyfilePath] == "" {
		t.Fatal("expected Caddyfile write")
	}
	if resource.createdFiles[sandbox.SandboxCertPath] == "" {
		t.Fatal("expected certificate write")
	}
	if resource.createdFiles[sandbox.SandboxCertKeyPath] == "" {
		t.Fatal("expected private key write")
	}
}

func TestSandboxCreatorStartsExistingStoppedInstance(t *testing.T) {
	plan := sandboxPlanForTest(t)
	resource := fakeSandboxResourceWithCA(t)
	resource.instance = &api.Instance{Name: plan.InstanceName, StatusCode: api.Stopped}
	creator := SandboxCreator{Server: fakeSandboxServer{resource: resource}}
	if err := creator.CreateSandbox(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if !resource.started {
		t.Fatal("expected stopped instance to be started")
	}
	if resource.createdFiles[sandbox.CaddyfilePath] == "" {
		t.Fatal("expected Caddyfile write")
	}
}

func sandboxPlanForTest(t *testing.T) sandbox.CreatePlan {
	t.Helper()
	projectConfig, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sandbox.PlanCreate(context.Background(), config.LoadAdminFromEnv(), project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-alice-myproject",
		Config: projectConfig,
	}}}, sandbox.CreateRequest{Reference: "alice/myproject/codex"})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func fakeSandboxResourceWithCA(t *testing.T) *fakeSandboxResource {
	t.Helper()
	ca, err := certs.GenerateCA("test CA", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return &fakeSandboxResource{caFiles: map[string]string{
		project.ProjectCACertPath: string(ca.CertificatePEM),
		project.ProjectCAKeyPath:  string(ca.PrivateKeyPEM),
	}}
}
