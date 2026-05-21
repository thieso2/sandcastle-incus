package incusx

import (
	"context"
	"io"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/caddy"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type fakeMachinePortServer struct {
	resource *fakeMachinePortResource
}

func (s fakeMachinePortServer) UseProject(name string) MachinePortResourceServer {
	return s.resource
}

type fakeMachinePortResource struct {
	instance     *api.Instance
	updated      *api.InstancePut
	createdFiles map[string]string
	execCommands [][]string
}

func (r *fakeMachinePortResource) GetInstance(name string) (*api.Instance, string, error) {
	return r.instance, "etag", nil
}

func (r *fakeMachinePortResource) UpdateInstance(name string, instance api.InstancePut, etag string) (incus.Operation, error) {
	r.updated = &instance
	return fakeOperation{}, nil
}

func (r *fakeMachinePortResource) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
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

func (r *fakeMachinePortResource) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	r.execCommands = append(r.execCommands, exec.Command)
	if args.DataDone != nil {
		close(args.DataDone)
	}
	return fakeOperation{}, nil
}

func TestMachinePortSetterUpdatesMetadata(t *testing.T) {
	config, err := meta.MachineConfig(meta.Machine{
		Tenant:    "acme",
		Project:   "default",
		Name:      "codex",
		AppPort:   3000,
		PrivateIP: "10.248.0.20",
	})
	if err != nil {
		t.Fatal(err)
	}
	resource := &fakeMachinePortResource{instance: &api.Instance{
		Name: "default-codex",
		InstancePut: api.InstancePut{
			Config: api.ConfigMap(config),
		},
	}}
	setter := MachinePortSetter{Server: fakeMachinePortServer{resource: resource}}
	err = setter.SetAppPort(context.Background(), machine.PortSetPlan{
		Reference:    "acme/default/codex",
		Tenant:       tenant.Summary{IncusName: "sc-acme"},
		Project:      "default",
		Name:         "codex",
		InstanceName: "default-codex",
		AppPort:      5173,
		CaddyFile:    caddy.RenderMachine("codex.default.acme", 5173, machine.MachineCertPath, machine.MachineCertKeyPath),
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
	parsed, err := meta.ParseMachineConfig(map[string]string(resource.updated.Config))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.AppPort != 5173 {
		t.Fatalf("state AppPort = %d", parsed.AppPort)
	}
	if resource.createdFiles[machine.CaddyfilePath] == "" {
		t.Fatal("expected Caddyfile write")
	}
	if len(resource.execCommands) != 1 || !strings.Contains(strings.Join(resource.execCommands[0], " "), "caddy") {
		t.Fatalf("exec commands = %#v", resource.execCommands)
	}
}
