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
	execs    []fakeMachineSSHKeyExec
	failAt   int
	failWith func(instanceName string) error
	exitCode func(instanceName string) int
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
	if r.failWith != nil {
		if err := r.failWith(instanceName); err != nil {
			return nil, err
		}
	}
	if r.failAt > 0 && len(r.execs) == r.failAt {
		return nil, errors.New("boom")
	}
	if args != nil && args.DataDone != nil {
		go func() { args.DataDone <- true }()
	}
	if r.exitCode != nil {
		return staticOperation{op: api.Operation{Metadata: map[string]any{"return": float64(r.exitCode(instanceName))}}}, nil
	}
	return staticOperation{}, nil
}

// Regression for #51: rotating the login key must reach machines that ALREADY
// exist. v2 machines carry the bare machine name and live in per-project Incus
// projects (<prefix>-<tenant>-<project>); the v1 "<project>-<machine>" name in
// the tenant's single project is what made this reconcile unusable for v2, so it
// was skipped entirely and rotation locked users out of every existing machine.
func TestMachineSSHKeyReconcilerV2UsesBareNamesAndPerProjectIncusProjects(t *testing.T) {
	resource := &fakeMachineSSHKeyResource{}
	server := &fakeMachineSSHKeyServer{resource: resource}
	reconciler := MachineSSHKeyReconciler{
		Store: fakeMachineSSHKeyStore{machines: []meta.Machine{
			{Tenant: "alice", Project: "default", Name: "web", Running: true},
			{Tenant: "alice", Project: "backend", Name: "api", Running: true},
		}},
		Server: server,
	}
	summary := v2Summary()
	if err := reconciler.ReconcileUserSSHKey(context.Background(), summary, "alice", "ssh-ed25519 rotated"); err != nil {
		t.Fatal(err)
	}
	wantProjects := []string{"sc2-alice-default", "sc2-alice-backend"}
	if strings.Join(server.resources, ",") != strings.Join(wantProjects, ",") {
		t.Fatalf("incus projects = %#v, want %#v", server.resources, wantProjects)
	}
	if len(resource.execs) != 2 || resource.execs[0].instance != "web" || resource.execs[1].instance != "api" {
		t.Fatalf("execs = %#v (v2 instance names are bare)", resource.execs)
	}
	// The Unix login user comes from the tenant, NOT from the GitHub user key:
	// writing to /home/<github-user> silently wrote nowhere.
	for _, exec := range resource.execs {
		if exec.environment["SANDCASTLE_USER"] != "sc" {
			t.Fatalf("SANDCASTLE_USER = %q, want the tenant Unix user %q", exec.environment["SANDCASTLE_USER"], "sc")
		}
		if exec.environment["SANDCASTLE_SSH_PUBLIC_KEY"] != "ssh-ed25519 rotated" {
			t.Fatalf("environment = %#v", exec.environment)
		}
	}
}

// A v2 project's machines share ONE home volume, so authorized_keys is a single
// file per project: one running machine is enough, and stopped machines are not
// even exec'd (they read the same file when they next boot).
func TestMachineSSHKeyReconcilerV2WritesOncePerProjectAndSkipsStopped(t *testing.T) {
	resource := &fakeMachineSSHKeyResource{}
	reconciler := MachineSSHKeyReconciler{
		Store: fakeMachineSSHKeyStore{machines: []meta.Machine{
			{Tenant: "alice", Project: "default", Name: "stopped", Running: false},
			{Tenant: "alice", Project: "default", Name: "running", Running: true},
			{Tenant: "alice", Project: "default", Name: "also-running", Running: true},
		}},
		Server: &fakeMachineSSHKeyServer{resource: resource},
	}
	if err := reconciler.ReconcileUserSSHKey(context.Background(), v2Summary(), "alice", "ssh-ed25519 rotated"); err != nil {
		t.Fatal(err)
	}
	if len(resource.execs) != 1 || resource.execs[0].instance != "running" {
		t.Fatalf("expected exactly one write to the shared home, got %#v", resource.execs)
	}
}

// Every machine stopped: nothing to write, and the login must still succeed —
// the key is already in the profile and lands when one next boots.
func TestMachineSSHKeyReconcilerV2ToleratesAnEntirelyStoppedProject(t *testing.T) {
	resource := &fakeMachineSSHKeyResource{}
	reconciler := MachineSSHKeyReconciler{
		Store: fakeMachineSSHKeyStore{machines: []meta.Machine{
			{Tenant: "alice", Project: "default", Name: "a", Running: false},
			{Tenant: "alice", Project: "default", Name: "b", Running: false},
		}},
		Server: &fakeMachineSSHKeyServer{resource: resource},
	}
	if err := reconciler.ReconcileUserSSHKey(context.Background(), v2Summary(), "alice", "ssh-ed25519 rotated"); err != nil {
		t.Fatalf("an all-stopped project must not fail the login: %v", err)
	}
	if len(resource.execs) != 0 {
		t.Fatalf("stopped machines must not be exec'd: %#v", resource.execs)
	}
}

// A machine that cannot be written must not be reported as reconciled. The other
// running machine in the project is tried instead.
func TestMachineSSHKeyReconcilerV2FallsBackToAnotherRunningMachine(t *testing.T) {
	resource := &fakeMachineSSHKeyResource{
		failWith: func(instanceName string) error {
			if instanceName == "agentless-vm" {
				return errors.New("Failed to connect to agent")
			}
			return nil
		},
	}
	reconciler := MachineSSHKeyReconciler{
		Store: fakeMachineSSHKeyStore{machines: []meta.Machine{
			{Tenant: "alice", Project: "default", Name: "agentless-vm", Running: true},
			{Tenant: "alice", Project: "default", Name: "ct", Running: true},
		}},
		Server: &fakeMachineSSHKeyServer{resource: resource},
	}
	if err := reconciler.ReconcileUserSSHKey(context.Background(), v2Summary(), "alice", "ssh-ed25519 rotated"); err != nil {
		t.Fatalf("a second running machine should have carried the write: %v", err)
	}
	if len(resource.execs) != 2 || resource.execs[1].instance != "ct" {
		t.Fatalf("execs = %#v", resource.execs)
	}
}

// When NO running machine in the project could be written, the failure surfaces
// rather than silently reporting success.
func TestMachineSSHKeyReconcilerV2SurfacesRealFailures(t *testing.T) {
	resource := &fakeMachineSSHKeyResource{
		failWith: func(string) error { return errors.New("permission denied writing authorized_keys") },
	}
	reconciler := MachineSSHKeyReconciler{
		Store: fakeMachineSSHKeyStore{machines: []meta.Machine{
			{Tenant: "alice", Project: "default", Name: "web", Running: true},
		}},
		Server: &fakeMachineSSHKeyServer{resource: resource},
	}
	err := reconciler.ReconcileUserSSHKey(context.Background(), v2Summary(), "alice", "ssh-ed25519 rotated")
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("error = %v", err)
	}
}

// incus exec reports a non-zero SCRIPT exit in the operation metadata, not from
// op.Wait(). Without reading it, a write that failed inside the machine (e.g.
// the target Unix user does not exist) looked exactly like success — which is
// how the broken v2 path went unnoticed.
func TestMachineSSHKeyReconcilerSurfacesNonZeroScriptExit(t *testing.T) {
	resource := &fakeMachineSSHKeyResource{exitCode: func(string) int { return 1 }}
	reconciler := MachineSSHKeyReconciler{
		Store: fakeMachineSSHKeyStore{machines: []meta.Machine{
			{Tenant: "alice", Project: "default", Name: "web", Running: true},
		}},
		Server: &fakeMachineSSHKeyServer{resource: resource},
	}
	err := reconciler.ReconcileUserSSHKey(context.Background(), v2Summary(), "alice", "ssh-ed25519 rotated")
	if err == nil || !strings.Contains(err.Error(), "script exited 1") {
		t.Fatalf("error = %v", err)
	}
}

func v2Summary() tenant.Summary {
	return tenant.Summary{
		Tenant:       "alice",
		Version:      2,
		InfraProject: "sc2-alice",
		IncusName:    "sc2-alice-default",
		UnixUser:     "sc",
	}
}
