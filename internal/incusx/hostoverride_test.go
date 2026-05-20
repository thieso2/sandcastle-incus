package incusx

import (
	"context"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type fakeHostOverrideServer struct {
	resource *fakeHostOverrideResource
}

func (s fakeHostOverrideServer) UseProject(name string) HostOverrideResourceServer {
	return s.resource
}

type fakeHostOverrideResource struct {
	instances []api.Instance
}

func (r *fakeHostOverrideResource) GetInstances(instanceType api.InstanceType) ([]api.Instance, error) {
	return r.instances, nil
}

func TestHostOverrideManagerFindsSandbox(t *testing.T) {
	configMap, err := meta.SandboxConfig(meta.Sandbox{
		Owner:     "alice",
		Project:   "myproject",
		Name:      "codex",
		AppPort:   3000,
		PrivateIP: "10.248.0.20",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := HostOverrideManager{Server: fakeHostOverrideServer{resource: &fakeHostOverrideResource{
		instances: []api.Instance{{Name: "sc-codex", InstancePut: api.InstancePut{Config: api.ConfigMap(configMap)}}},
	}}}
	sandbox, err := manager.FindSandbox(context.Background(), project.Summary{
		IncusName: "sc-alice-myproject",
		Owner:     "alice",
		Name:      "myproject",
	}, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if sandbox.PrivateIP != "10.248.0.20" {
		t.Fatalf("PrivateIP = %q", sandbox.PrivateIP)
	}
}
