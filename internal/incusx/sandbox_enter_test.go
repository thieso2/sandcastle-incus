package incusx

import (
	"context"
	"io"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
)

type fakeSandboxEnterServer struct {
	resource *fakeSandboxEnterResource
}

func (s fakeSandboxEnterServer) UseProject(name string) SandboxEnterResourceServer {
	return s.resource
}

type fakeSandboxEnterResource struct {
	instanceName string
	exec         api.InstanceExecPost
}

func (r *fakeSandboxEnterResource) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	r.instanceName = instanceName
	r.exec = exec
	if args.DataDone != nil {
		close(args.DataDone)
	}
	return fakeOperation{}, nil
}

func TestSandboxEntererExecsInteractiveShell(t *testing.T) {
	resource := &fakeSandboxEnterResource{}
	enterer := SandboxEnterer{Server: fakeSandboxEnterServer{resource: resource}}
	err := enterer.EnterSandbox(context.Background(), sandbox.EnterPlan{
		Project:      project.Summary{IncusName: "sc-alice-myproject"},
		InstanceName: "sc-codex",
		Command:      []string{"/bin/bash", "-l"},
		WorkingDir:   "/workspace",
		Interactive:  true,
	}, sandbox.EnterSession{
		Stdin:  io.Reader(nil),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resource.instanceName != "sc-codex" {
		t.Fatalf("instanceName = %q", resource.instanceName)
	}
	if !resource.exec.Interactive {
		t.Fatal("expected interactive exec")
	}
	if resource.exec.RecordOutput {
		t.Fatal("interactive exec should not record output")
	}
	if resource.exec.Cwd != "/workspace" {
		t.Fatalf("Cwd = %q", resource.exec.Cwd)
	}
}

func TestSandboxEntererExecsCommandNonInteractively(t *testing.T) {
	resource := &fakeSandboxEnterResource{}
	enterer := SandboxEnterer{Server: fakeSandboxEnterServer{resource: resource}}
	err := enterer.EnterSandbox(context.Background(), sandbox.EnterPlan{
		Project:      project.Summary{IncusName: "sc-alice-myproject"},
		InstanceName: "sc-codex",
		Command:      []string{"pwd"},
		WorkingDir:   "/workspace",
		Interactive:  false,
	}, sandbox.EnterSession{
		Stdin:  io.Reader(nil),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resource.exec.Interactive {
		t.Fatal("expected non-interactive exec")
	}
	if !resource.exec.RecordOutput {
		t.Fatal("non-interactive exec should record output")
	}
}
