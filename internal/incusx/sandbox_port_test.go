package incusx

import (
	"context"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
)

type fakeSandboxPortServer struct {
	resource *fakeSandboxPortResource
}

func (s fakeSandboxPortServer) UseProject(name string) SandboxPortResourceServer {
	return s.resource
}

type fakeSandboxPortResource struct {
	instance *api.Instance
	updated  *api.InstancePut
}

func (r *fakeSandboxPortResource) GetInstance(name string) (*api.Instance, string, error) {
	return r.instance, "etag", nil
}

func (r *fakeSandboxPortResource) UpdateInstance(name string, instance api.InstancePut, etag string) (incus.Operation, error) {
	r.updated = &instance
	return fakeOperation{}, nil
}

func TestSandboxPortSetterUpdatesMetadata(t *testing.T) {
	config, err := meta.SandboxConfig(meta.Sandbox{
		Owner:     "alice",
		Project:   "myproject",
		Name:      "codex",
		AppPort:   3000,
		PrivateIP: "10.248.0.20",
	})
	if err != nil {
		t.Fatal(err)
	}
	resource := &fakeSandboxPortResource{instance: &api.Instance{
		Name: "sc-codex",
		InstancePut: api.InstancePut{
			Config: api.ConfigMap(config),
		},
	}}
	setter := SandboxPortSetter{Server: fakeSandboxPortServer{resource: resource}}
	err = setter.SetAppPort(context.Background(), sandbox.PortSetPlan{
		Reference:    "alice/myproject/codex",
		Project:      project.Summary{IncusName: "sc-alice-myproject"},
		Name:         "codex",
		InstanceName: "sc-codex",
		AppPort:      5173,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resource.updated == nil {
		t.Fatal("expected instance update")
	}
	if resource.updated.Config[meta.KeyAppPort] != "5173" {
		t.Fatalf("app port scalar = %q", resource.updated.Config[meta.KeyAppPort])
	}
	parsed, err := meta.ParseSandboxConfig(map[string]string(resource.updated.Config))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.AppPort != 5173 {
		t.Fatalf("state AppPort = %d", parsed.AppPort)
	}
}
