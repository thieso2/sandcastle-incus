package incusx

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type fakeMachineLifecycleServer struct {
	resource *fakeMachineLifecycleResource
}

func (s fakeMachineLifecycleServer) UseProject(name string) MachineLifecycleResourceServer {
	return s.resource
}

type fakeMachineLifecycleResource struct {
	stateActions []string
	deleted      string
	deleteErr    error
	instance     *api.Instance
	network      *api.Network
	execScripts  []string
}

func (r *fakeMachineLifecycleResource) UpdateInstanceState(name string, state api.InstanceStatePut, etag string) (incus.Operation, error) {
	r.stateActions = append(r.stateActions, state.Action)
	return fakeOperation{}, nil
}

func (r *fakeMachineLifecycleResource) DeleteInstance(name string) (incus.Operation, error) {
	r.deleted = name
	if r.deleteErr != nil {
		return nil, r.deleteErr
	}
	return fakeOperation{}, nil
}

func (r *fakeMachineLifecycleResource) GetInstance(name string) (*api.Instance, string, error) {
	if r.instance != nil {
		return r.instance, "", nil
	}
	return &api.Instance{}, "", nil
}

func (r *fakeMachineLifecycleResource) GetNetwork(name string) (*api.Network, string, error) {
	if r.network != nil {
		return r.network, "", nil
	}
	return &api.Network{}, "", nil
}

func (r *fakeMachineLifecycleResource) ExecInstance(name string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	if args != nil && args.Stdin != nil {
		buf, _ := io.ReadAll(args.Stdin)
		r.execScripts = append(r.execScripts, string(buf))
	}
	if args != nil && args.DataDone != nil {
		close(args.DataDone)
	}
	return fakeOperation{}, nil
}

func TestMachineControllerAppliesStateActions(t *testing.T) {
	for _, tc := range []struct {
		action machine.Action
		want   string
	}{
		{machine.ActionStart, "start"},
		{machine.ActionStop, "stop"},
		{machine.ActionRestart, "restart"},
	} {
		resource := &fakeMachineLifecycleResource{}
		controller := MachineController{Server: fakeMachineLifecycleServer{resource: resource}}
		if err := controller.ApplyLifecycle(context.Background(), machineLifecyclePlan(tc.action)); err != nil {
			t.Fatal(err)
		}
		if len(resource.stateActions) != 1 || resource.stateActions[0] != tc.want {
			t.Fatalf("action %q produced %#v", tc.action, resource.stateActions)
		}
	}
}

func TestMachineControllerHealsNetworkOnStart(t *testing.T) {
	resource := &fakeMachineLifecycleResource{
		instance: &api.Instance{InstancePut: api.InstancePut{Devices: map[string]map[string]string{
			"eth0": {"ipv4.address": "10.248.1.23", "parent": "sc-acme"},
		}}},
		network: &api.Network{NetworkPut: api.NetworkPut{Config: map[string]string{"ipv4.address": "10.248.1.1/24"}}},
	}
	controller := MachineController{Server: fakeMachineLifecycleServer{resource: resource}}
	for _, action := range []machine.Action{machine.ActionStart, machine.ActionRestart} {
		resource.execScripts = nil
		if err := controller.ApplyLifecycle(context.Background(), machineLifecyclePlan(action)); err != nil {
			t.Fatal(err)
		}
		if len(resource.execScripts) != 1 {
			t.Fatalf("%s: expected 1 heal exec, got %d", action, len(resource.execScripts))
		}
		script := resource.execScripts[0]
		if !strings.Contains(script, "ip addr replace 10.248.1.23/24 dev eth0") {
			t.Fatalf("%s: heal script missing IP apply:\n%s", action, script)
		}
		if !strings.Contains(script, "ip route replace default via 10.248.1.1") {
			t.Fatalf("%s: heal script missing gateway:\n%s", action, script)
		}
		if !strings.Contains(script, "multi-user.target.wants/sandcastle-machine-network.service") {
			t.Fatalf("%s: heal script missing enable symlink:\n%s", action, script)
		}
	}
}

func TestMachineControllerStopDoesNotHeal(t *testing.T) {
	resource := &fakeMachineLifecycleResource{
		instance: &api.Instance{InstancePut: api.InstancePut{Devices: map[string]map[string]string{
			"eth0": {"ipv4.address": "10.248.1.23", "parent": "sc-acme"},
		}}},
		network: &api.Network{NetworkPut: api.NetworkPut{Config: map[string]string{"ipv4.address": "10.248.1.1/24"}}},
	}
	controller := MachineController{Server: fakeMachineLifecycleServer{resource: resource}}
	if err := controller.ApplyLifecycle(context.Background(), machineLifecyclePlan(machine.ActionStop)); err != nil {
		t.Fatal(err)
	}
	if len(resource.execScripts) != 0 {
		t.Fatalf("stop should not heal, got %d execs", len(resource.execScripts))
	}
}

func TestMachineControllerRemovesInstance(t *testing.T) {
	resource := &fakeMachineLifecycleResource{}
	controller := MachineController{Server: fakeMachineLifecycleServer{resource: resource}}
	if err := controller.ApplyLifecycle(context.Background(), machineLifecyclePlan(machine.ActionDelete)); err != nil {
		t.Fatal(err)
	}
	if resource.deleted != "default-codex" {
		t.Fatalf("deleted = %q", resource.deleted)
	}
}

func TestMachineControllerRemoveIgnoresMissingInstance(t *testing.T) {
	resource := &fakeMachineLifecycleResource{deleteErr: api.StatusErrorf(http.StatusNotFound, "not found")}
	controller := MachineController{Server: fakeMachineLifecycleServer{resource: resource}}
	if err := controller.ApplyLifecycle(context.Background(), machineLifecyclePlan(machine.ActionDelete)); err != nil {
		t.Fatal(err)
	}
}

func machineLifecyclePlan(action machine.Action) machine.LifecyclePlan {
	return machine.LifecyclePlan{
		Reference:    "acme/default/codex",
		Name:         "codex",
		InstanceName: "default-codex",
		Action:       action,
		Tenant:       tenant.Summary{IncusName: "sc-acme", Tenant: "acme"},
		Project:      "default",
	}
}
