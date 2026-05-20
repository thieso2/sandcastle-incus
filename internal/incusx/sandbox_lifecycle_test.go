package incusx

import (
	"context"
	"net/http"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
)

type fakeSandboxLifecycleServer struct {
	resource *fakeSandboxLifecycleResource
}

func (s fakeSandboxLifecycleServer) UseProject(name string) SandboxLifecycleResourceServer {
	return s.resource
}

type fakeSandboxLifecycleResource struct {
	stateActions []string
	deleted      string
	deleteErr    error
}

func (r *fakeSandboxLifecycleResource) UpdateInstanceState(name string, state api.InstanceStatePut, etag string) (incus.Operation, error) {
	r.stateActions = append(r.stateActions, state.Action)
	return fakeOperation{}, nil
}

func (r *fakeSandboxLifecycleResource) DeleteInstance(name string) (incus.Operation, error) {
	r.deleted = name
	if r.deleteErr != nil {
		return nil, r.deleteErr
	}
	return fakeOperation{}, nil
}

func TestSandboxControllerAppliesStateActions(t *testing.T) {
	for _, tc := range []struct {
		action sandbox.Action
		want   string
	}{
		{sandbox.ActionStart, "start"},
		{sandbox.ActionStop, "stop"},
		{sandbox.ActionRestart, "restart"},
	} {
		resource := &fakeSandboxLifecycleResource{}
		controller := SandboxController{Server: fakeSandboxLifecycleServer{resource: resource}}
		if err := controller.ApplyLifecycle(context.Background(), sandboxLifecyclePlan(tc.action)); err != nil {
			t.Fatal(err)
		}
		if len(resource.stateActions) != 1 || resource.stateActions[0] != tc.want {
			t.Fatalf("action %q produced %#v", tc.action, resource.stateActions)
		}
	}
}

func TestSandboxControllerRemovesInstance(t *testing.T) {
	resource := &fakeSandboxLifecycleResource{}
	controller := SandboxController{Server: fakeSandboxLifecycleServer{resource: resource}}
	if err := controller.ApplyLifecycle(context.Background(), sandboxLifecyclePlan(sandbox.ActionRemove)); err != nil {
		t.Fatal(err)
	}
	if resource.deleted != "sc-codex" {
		t.Fatalf("deleted = %q", resource.deleted)
	}
}

func TestSandboxControllerRemoveIgnoresMissingInstance(t *testing.T) {
	resource := &fakeSandboxLifecycleResource{deleteErr: api.StatusErrorf(http.StatusNotFound, "not found")}
	controller := SandboxController{Server: fakeSandboxLifecycleServer{resource: resource}}
	if err := controller.ApplyLifecycle(context.Background(), sandboxLifecyclePlan(sandbox.ActionRemove)); err != nil {
		t.Fatal(err)
	}
}

func sandboxLifecyclePlan(action sandbox.Action) sandbox.LifecyclePlan {
	return sandbox.LifecyclePlan{
		Reference:    "alice/myproject/codex",
		Name:         "codex",
		InstanceName: "sc-codex",
		Action:       action,
		Project: project.Summary{
			IncusName: "sc-alice-myproject",
			Owner:     "alice",
			Name:      "myproject",
		},
	}
}
