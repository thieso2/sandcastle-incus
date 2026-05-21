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
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type fakeSandboxServer struct {
	resource *fakeSandboxResource
}

func (s fakeSandboxServer) UseProject(name string) SandboxResourceServer {
	return s.resource
}

type fakeSandboxResource struct {
	instance          *api.Instance
	created           *api.InstancesPost
	started           bool
	createdFiles      map[string]string
	createdVolumeDirs []string
	caFiles           map[string]string
	execCommands      [][]string
	execEnvs          []map[string]string
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

func (r *fakeSandboxResource) CreateStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error {
	if args.Type != "directory" {
		return api.StatusErrorf(http.StatusBadRequest, "unexpected volume file type")
	}
	if r.created != nil {
		return api.StatusErrorf(http.StatusBadRequest, "volume directory created after instance")
	}
	r.createdVolumeDirs = append(r.createdVolumeDirs, volumeName+"/"+filePath)
	return nil
}

func (r *fakeSandboxResource) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	content, ok := r.caFiles[filePath]
	if !ok {
		return nil, nil, api.StatusErrorf(http.StatusNotFound, "not found")
	}
	return io.NopCloser(strings.NewReader(content)), &incus.InstanceFileResponse{Type: "file"}, nil
}

func (r *fakeSandboxResource) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	r.execCommands = append(r.execCommands, exec.Command)
	r.execEnvs = append(r.execEnvs, exec.Environment)
	if args.DataDone != nil {
		close(args.DataDone)
	}
	return fakeOperation{}, nil
}

func TestSandboxCreatorCreatesInstance(t *testing.T) {
	plan := sandboxPlanForTest(t)
	resource := fakeSandboxResourceWithCA(t)
	creator := SandboxCreator{Server: fakeSandboxServer{resource: resource}}
	if err := creator.CreateMachine(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if resource.created == nil {
		t.Fatal("expected instance creation")
	}
	if resource.created.Name != "default-codex" {
		t.Fatalf("created name = %q", resource.created.Name)
	}
	if resource.created.Devices["eth0"]["ipv4.address"] != "10.248.0.20" {
		t.Fatalf("devices = %#v", resource.created.Devices)
	}
	if resource.created.Devices["home"]["path"] != "/home/acme" {
		t.Fatalf("home device = %#v", resource.created.Devices["home"])
	}
	wantVolumeDirs := []string{"sc-home/default/codex", "sc-workspace/default/codex"}
	if strings.Join(resource.createdVolumeDirs, ",") != strings.Join(wantVolumeDirs, ",") {
		t.Fatalf("created volume dirs = %#v, want %#v", resource.createdVolumeDirs, wantVolumeDirs)
	}
	if resource.created.Config["environment.SANDCASTLE_USER"] != "acme" {
		t.Fatalf("instance config = %#v", resource.created.Config)
	}
	if _, ok := resource.created.Config["security.nesting"]; ok {
		t.Fatalf("security.nesting is set by default: %#v", resource.created.Config)
	}
	if _, ok := resource.created.Config["security.privileged"]; ok {
		t.Fatalf("security.privileged is set by default: %#v", resource.created.Config)
	}
	if resource.createdFiles[sandbox.CaddyfilePath] == "" {
		t.Fatal("expected Caddyfile write")
	}
	if resource.createdFiles[sandbox.MachineCertPath] == "" {
		t.Fatal("expected certificate write")
	}
	if resource.createdFiles[sandbox.MachineCertKeyPath] == "" {
		t.Fatal("expected private key write")
	}
	if len(resource.execCommands) != 2 {
		t.Fatalf("exec commands = %#v", resource.execCommands)
	}
	if strings.Join(resource.execCommands[0], " ") != "/usr/local/bin/sandcastle-bootstrap" {
		t.Fatalf("bootstrap command = %#v", resource.execCommands[0])
	}
	if resource.execEnvs[0]["SANDCASTLE_USER"] != "acme" {
		t.Fatalf("bootstrap env = %#v", resource.execEnvs[0])
	}
	if resource.execEnvs[0]["SANDCASTLE_UID"] != "1000" || resource.execEnvs[0]["SANDCASTLE_GID"] != "1000" {
		t.Fatalf("bootstrap uid/gid env = %#v", resource.execEnvs[0])
	}
	if !strings.Contains(strings.Join(resource.execCommands[1], " "), "caddy") {
		t.Fatalf("caddy command = %#v", resource.execCommands[1])
	}
}

func TestSandboxCreatorEnablesNestingForContainerTools(t *testing.T) {
	plan := sandboxPlanForTest(t)
	plan.ContainerTools = true
	resource := fakeSandboxResourceWithCA(t)
	creator := SandboxCreator{Server: fakeSandboxServer{resource: resource}}
	if err := creator.CreateMachine(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if resource.created == nil {
		t.Fatal("expected instance creation")
	}
	if resource.created.Config["security.nesting"] != "true" {
		t.Fatalf("security.nesting = %q", resource.created.Config["security.nesting"])
	}
	if _, ok := resource.created.Config["security.privileged"]; ok {
		t.Fatalf("security.privileged is set: %#v", resource.created.Config)
	}
}

func TestSandboxCreatorStartsExistingStoppedInstance(t *testing.T) {
	plan := sandboxPlanForTest(t)
	resource := fakeSandboxResourceWithCA(t)
	resource.instance = &api.Instance{Name: plan.InstanceName, StatusCode: api.Stopped}
	creator := SandboxCreator{Server: fakeSandboxServer{resource: resource}}
	if err := creator.CreateMachine(context.Background(), plan); err != nil {
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
	projectConfig, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	plan, err := sandbox.PlanCreate(context.Background(), admin, project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-acme",
		Config: projectConfig,
	}}}, nil, sandbox.CreateRequest{Reference: "codex"})
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
		project.TenantCACertPath: string(ca.CertificatePEM),
		project.TenantCAKeyPath:  string(ca.PrivateKeyPEM),
	}}
}
