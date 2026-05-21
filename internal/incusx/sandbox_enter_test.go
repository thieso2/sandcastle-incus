package incusx

import (
	"context"
	"io"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
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
	err := enterer.ConnectMachine(context.Background(), sandbox.EnterPlan{
		Tenant:       project.Summary{IncusName: "sc-acme"},
		Project:      "default",
		InstanceName: "default-codex",
		Command:      []string{"/bin/bash", "-l"},
		LinuxUser:    "alice",
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
	if resource.instanceName != "default-codex" {
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
	if resource.exec.User != sandbox.DefaultLinuxUID || resource.exec.Group != sandbox.DefaultLinuxGID {
		t.Fatalf("user/group = %d/%d", resource.exec.User, resource.exec.Group)
	}
	if resource.exec.Environment["HOME"] != "/home/alice" || resource.exec.Environment["USER"] != "alice" {
		t.Fatalf("environment = %#v", resource.exec.Environment)
	}
}

func TestSandboxEntererExecsCommandNonInteractively(t *testing.T) {
	resource := &fakeSandboxEnterResource{}
	enterer := SandboxEnterer{Server: fakeSandboxEnterServer{resource: resource}}
	err := enterer.ConnectMachine(context.Background(), sandbox.EnterPlan{
		Tenant:       project.Summary{IncusName: "sc-acme"},
		Project:      "default",
		InstanceName: "default-codex",
		Command:      []string{"pwd"},
		LinuxUser:    "alice",
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
	if resource.exec.RecordOutput {
		t.Fatal("exec should not record output (incompatible with WaitForWS in Incus 7.0)")
	}
}
