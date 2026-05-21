package incusx

import (
	"context"
	"io"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type fakeMachineConnectServer struct {
	resource *fakeMachineConnectResource
}

func (s fakeMachineConnectServer) UseProject(name string) MachineConnectResourceServer {
	return s.resource
}

type fakeMachineConnectResource struct {
	instanceName string
	exec         api.InstanceExecPost
}

func (r *fakeMachineConnectResource) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	r.instanceName = instanceName
	r.exec = exec
	if args.DataDone != nil {
		close(args.DataDone)
	}
	return fakeOperation{}, nil
}

func TestMachineConnectorExecsInteractiveShell(t *testing.T) {
	resource := &fakeMachineConnectResource{}
	connector := MachineConnector{Server: fakeMachineConnectServer{resource: resource}}
	err := connector.ConnectMachine(context.Background(), machine.ConnectPlan{
		Tenant:       tenant.Summary{IncusName: "sc-acme"},
		Project:      "default",
		InstanceName: "default-codex",
		Command:      []string{"/bin/bash", "-l"},
		LinuxUser:    "alice",
		WorkingDir:   "/workspace",
		Interactive:  true,
	}, machine.ConnectSession{
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
	if resource.exec.User != machine.DefaultLinuxUID || resource.exec.Group != machine.DefaultLinuxGID {
		t.Fatalf("user/group = %d/%d", resource.exec.User, resource.exec.Group)
	}
	if resource.exec.Environment["HOME"] != "/home/alice" || resource.exec.Environment["USER"] != "alice" {
		t.Fatalf("environment = %#v", resource.exec.Environment)
	}
}

func TestMachineConnectorExecsCommandNonInteractively(t *testing.T) {
	resource := &fakeMachineConnectResource{}
	connector := MachineConnector{Server: fakeMachineConnectServer{resource: resource}}
	err := connector.ConnectMachine(context.Background(), machine.ConnectPlan{
		Tenant:       tenant.Summary{IncusName: "sc-acme"},
		Project:      "default",
		InstanceName: "default-codex",
		Command:      []string{"pwd"},
		LinuxUser:    "alice",
		WorkingDir:   "/workspace",
		Interactive:  false,
	}, machine.ConnectSession{
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
