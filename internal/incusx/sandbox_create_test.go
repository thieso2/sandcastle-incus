package incusx

import (
	"context"
	"net/http"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
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
	instance *api.Instance
	created  *api.InstancesPost
	started  bool
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

func TestSandboxCreatorCreatesInstance(t *testing.T) {
	plan := sandboxPlanForTest(t)
	resource := &fakeSandboxResource{}
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
}

func TestSandboxCreatorStartsExistingStoppedInstance(t *testing.T) {
	plan := sandboxPlanForTest(t)
	resource := &fakeSandboxResource{instance: &api.Instance{Name: plan.InstanceName, StatusCode: api.Stopped}}
	creator := SandboxCreator{Server: fakeSandboxServer{resource: resource}}
	if err := creator.CreateSandbox(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if !resource.started {
		t.Fatal("expected stopped instance to be started")
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
