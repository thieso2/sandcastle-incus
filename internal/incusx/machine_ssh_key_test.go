package incusx

import (
	"context"
	"errors"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestMachineSSHKeyReconcilerSkipsWhenNoMachinesExist(t *testing.T) {
	server := &fakeMachineSSHKeyServer{}
	reconciler := MachineSSHKeyReconciler{
		Store:  fakeMachineSSHKeyStore{},
		Server: server,
	}
	if err := reconciler.ReconcileUserSSHKey(context.Background(), tenant.Summary{Tenant: "alice", IncusName: "sc-alice"}, "alice", "ssh-ed25519 key"); err != nil {
		t.Fatal(err)
	}
	if len(server.resources) != 0 {
		t.Fatalf("server resources = %#v", server.resources)
	}
}

func TestMachineSSHKeyReconcilerUpdatesExistingMachines(t *testing.T) {
	resource := &fakeMachineSSHKeyResource{}
	reconciler := MachineSSHKeyReconciler{
		Store: fakeMachineSSHKeyStore{machines: []meta.Machine{
			{Tenant: "alice", Project: "default", Name: "dev", LinuxUser: "alice"},
			{Tenant: "alice", Project: "work", Name: "api", LinuxUser: "alice"},
		}},
		Server: &fakeMachineSSHKeyServer{resource: resource},
	}
	if err := reconciler.ReconcileUserSSHKey(context.Background(), tenant.Summary{Tenant: "alice", IncusName: "sc-alice"}, "alice", "ssh-ed25519 key"); err != nil {
		t.Fatal(err)
	}
	if len(resource.execs) != 2 || resource.execs[0].instance != "default-dev" || resource.execs[1].instance != "work-api" {
		t.Fatalf("execs = %#v", resource.execs)
	}
	for _, exec := range resource.execs {
		if exec.environment["SANDCASTLE_SSH_PUBLIC_KEY"] != "ssh-ed25519 key" || exec.environment["SANDCASTLE_USER"] != "alice" {
			t.Fatalf("environment = %#v", exec.environment)
		}
		script := strings.Join(exec.command, " ")
		for _, want := range []string{"sandcastle user ssh key begin", "authorized_keys", "awk"} {
			if !strings.Contains(script, want) {
				t.Fatalf("script missing %q: %s", want, script)
			}
		}
	}
}

func TestMachineSSHKeyReconcilerCanRetryAfterPartialFailure(t *testing.T) {
	resource := &fakeMachineSSHKeyResource{failAt: 2}
	reconciler := MachineSSHKeyReconciler{
		Store: fakeMachineSSHKeyStore{machines: []meta.Machine{
			{Tenant: "alice", Project: "default", Name: "one", LinuxUser: "alice"},
			{Tenant: "alice", Project: "default", Name: "two", LinuxUser: "alice"},
		}},
		Server: &fakeMachineSSHKeyServer{resource: resource},
	}
	err := reconciler.ReconcileUserSSHKey(context.Background(), tenant.Summary{Tenant: "alice", IncusName: "sc-alice"}, "alice", "ssh-ed25519 key")
	if err == nil || !strings.Contains(err.Error(), "default-two") {
		t.Fatalf("error = %v", err)
	}
	resource.failAt = 0
	if err := reconciler.ReconcileUserSSHKey(context.Background(), tenant.Summary{Tenant: "alice", IncusName: "sc-alice"}, "alice", "ssh-ed25519 key"); err != nil {
		t.Fatal(err)
	}
	if len(resource.execs) != 4 {
		t.Fatalf("execs after retry = %#v", resource.execs)
	}
}

func TestMachineSSHKeyReconcilerRevokesExistingMachineAccess(t *testing.T) {
	resource := &fakeMachineSSHKeyResource{}
	reconciler := MachineSSHKeyReconciler{
		Store: fakeMachineSSHKeyStore{machines: []meta.Machine{
			{Tenant: "alice", Project: "default", Name: "dev", LinuxUser: "alice"},
		}},
		Server: &fakeMachineSSHKeyServer{resource: resource},
	}
	if err := reconciler.RevokeUserSSHKey(context.Background(), tenant.Summary{Tenant: "alice", IncusName: "sc-alice"}, "alice"); err != nil {
		t.Fatal(err)
	}
	if len(resource.execs) != 1 || resource.execs[0].instance != "default-dev" {
		t.Fatalf("execs = %#v", resource.execs)
	}
	if resource.execs[0].environment["SANDCASTLE_SSH_PUBLIC_KEY"] != "" || resource.execs[0].environment["SANDCASTLE_USER"] != "alice" {
		t.Fatalf("environment = %#v", resource.execs[0].environment)
	}
	script := strings.Join(resource.execs[0].command, " ")
	if !strings.Contains(script, "sandcastle user ssh key begin") || strings.Contains(script, "SANDCASTLE_SSH_PUBLIC_KEY") {
		t.Fatalf("revoke script = %s", script)
	}
}

func TestMachineSSHKeyReconcilerRevokeSkipsWhenNoMachinesExist(t *testing.T) {
	server := &fakeMachineSSHKeyServer{}
	reconciler := MachineSSHKeyReconciler{
		Store:  fakeMachineSSHKeyStore{},
		Server: server,
	}
	if err := reconciler.RevokeUserSSHKey(context.Background(), tenant.Summary{Tenant: "alice", IncusName: "sc-alice"}, "alice"); err != nil {
		t.Fatal(err)
	}
	if len(server.resources) != 0 {
		t.Fatalf("server resources = %#v", server.resources)
	}
}

type fakeMachineSSHKeyStore struct {
	machines []meta.Machine
}

func (s fakeMachineSSHKeyStore) ListMachines(ctx context.Context, summary tenant.Summary) ([]meta.Machine, error) {
	return append([]meta.Machine{}, s.machines...), nil
}

type fakeMachineSSHKeyServer struct {
	resource  *fakeMachineSSHKeyResource
	resources []string
}

func (s *fakeMachineSSHKeyServer) UseProject(name string) MachineSSHKeyResourceServer {
	s.resources = append(s.resources, name)
	if s.resource == nil {
		s.resource = &fakeMachineSSHKeyResource{}
	}
	return s.resource
}

type fakeMachineSSHKeyResource struct {
	execs  []fakeMachineSSHKeyExec
	failAt int
}

type fakeMachineSSHKeyExec struct {
	instance    string
	command     []string
	environment map[string]string
}

func (r *fakeMachineSSHKeyResource) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	r.execs = append(r.execs, fakeMachineSSHKeyExec{
		instance:    instanceName,
		command:     append([]string{}, exec.Command...),
		environment: exec.Environment,
	})
	if r.failAt > 0 && len(r.execs) == r.failAt {
		return nil, errors.New("boom")
	}
	if args != nil && args.DataDone != nil {
		go func() { args.DataDone <- true }()
	}
	return staticOperation{}, nil
}
