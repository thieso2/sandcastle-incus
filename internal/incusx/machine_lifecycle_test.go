package incusx

import (
	"context"
	"net/http"
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
