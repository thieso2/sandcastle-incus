package incusx

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
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

type fakeSSHRunner struct {
	args []string
}

func (r *fakeSSHRunner) Run(ctx context.Context, session machine.ConnectSession, args ...string) error {
	r.args = append([]string{}, args...)
	return nil
}

func TestMachineConnectorSSHsToManagedMachine(t *testing.T) {
	runner := &fakeSSHRunner{}
	var logs []string
	connector := MachineConnector{
		Runner: runner,
		Log: func(msg string) {
			logs = append(logs, msg)
		},
	}
	err := connector.ConnectMachine(context.Background(), machine.ConnectPlan{
		Tenant:       tenant.Summary{IncusName: "sc-acme"},
		Project:      "default",
		InstanceName: "default-codex",
		SSHHost:      "10.248.0.20",
		HostKeyAlias: "codex.default.acme",
		Command:      []string{"/bin/bash", "-l"},
		LinuxUser:    "alice",
		UserID:       machine.DefaultLinuxUID,
		GroupID:      machine.DefaultLinuxGID,
		WorkingDir:   "/workspace",
		Interactive:  true,
		Managed:      true,
	}, machine.ConnectSession{
		Stdin:  io.Reader(nil),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.args, " ")
	for _, want := range []string{
		"-A",
		"CheckHostIP=no",
		"StrictHostKeyChecking=accept-new",
		"HostKeyAlias=codex.default.acme",
		"-t",
		"alice@10.248.0.20",
		"cd /workspace && exec /bin/bash -l",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ssh args %q missing %q", joined, want)
		}
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "ssh -A") || !strings.Contains(logs[0], "alice@10.248.0.20") || !strings.Contains(logs[0], `"cd /workspace && exec /bin/bash -l"`) {
		t.Fatalf("logs = %#v", logs)
	}
}

func TestMachineConnectorAddsSSHVerboseWhenVerboseEnabled(t *testing.T) {
	runner := &fakeSSHRunner{}
	connector := MachineConnector{Runner: runner}.WithVerbose(true, io.Discard)
	err := connector.ConnectMachine(context.Background(), machine.ConnectPlan{
		Tenant:       tenant.Summary{IncusName: "sc-acme"},
		Project:      "default",
		InstanceName: "default-codex",
		SSHHost:      "10.248.0.20",
		HostKeyAlias: "codex.default.acme",
		Command:      []string{"/bin/bash", "-l"},
		LinuxUser:    "alice",
		Interactive:  true,
		Managed:      true,
	}, machine.ConnectSession{
		Stdin:  io.Reader(nil),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range runner.args {
		if arg == "-v" {
			return
		}
	}
	t.Fatalf("ssh args missing -v: %#v", runner.args)
}

func TestMachineConnectorPinsCachedSSHIdentity(t *testing.T) {
	runner := &fakeSSHRunner{}
	cache := ConnectCache{path: filepath.Join(t.TempDir(), "connect-cache.json")}
	plan := machine.ConnectPlan{
		Tenant:       tenant.Summary{Tenant: "acme", IncusName: "sc-acme"},
		Project:      "default",
		Name:         "codex",
		InstanceName: "default-codex",
		SSHHost:      "10.248.0.20",
		HostKeyAlias: "codex.default.acme",
		Command:      []string{"/bin/bash", "-l"},
		LinuxUser:    "alice",
		Interactive:  true,
		Managed:      true,
	}
	identityPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(identityPath, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache.StoreSSHIdentity(sshIdentityCacheKey(plan), identityPath)
	connector := MachineConnector{Runner: runner}.WithConnectCache(cache)
	if err := connector.ConnectMachine(context.Background(), plan, machine.ConnectSession{Stdin: io.Reader(nil), Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.args, " ")
	if !strings.Contains(joined, "IdentitiesOnly=yes") || !strings.Contains(joined, "-i "+identityPath) {
		t.Fatalf("ssh args = %q", joined)
	}
}

func TestSSHIdentityCandidatesPreferEd25519(t *testing.T) {
	candidates := prioritizeSSHIdentityCandidates([]string{
		"/Users/alice/.ssh/id_rsa",
		"/Users/alice/.ssh/id_ecdsa",
		"/Users/alice/.ssh/id_ed25519",
	})
	if candidates[0] != "/Users/alice/.ssh/id_ed25519" {
		t.Fatalf("candidates = %#v", candidates)
	}
}

func TestMachineConnectorExecsUnmanagedMachineAsRoot(t *testing.T) {
	resource := &fakeMachineConnectResource{}
	connector := MachineConnector{Server: fakeMachineConnectServer{resource: resource}}
	err := connector.ConnectMachine(context.Background(), machine.ConnectPlan{
		Tenant:       tenant.Summary{IncusName: "sc-acme"},
		InstanceName: "sc-dns",
		Command:      []string{"/bin/bash", "-l"},
		LinuxUser:    "root",
		WorkingDir:   "/root",
		Interactive:  true,
		Managed:      false,
	}, machine.ConnectSession{
		Stdin:  io.Reader(nil),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resource.exec.User != 0 || resource.exec.Group != 0 {
		t.Fatalf("user/group = %d/%d", resource.exec.User, resource.exec.Group)
	}
	if resource.exec.Cwd != "/root" || resource.exec.Environment["HOME"] != "/root" || resource.exec.Environment["USER"] != "root" {
		t.Fatalf("exec = %#v", resource.exec)
	}
}

func TestMachineConnectorSSHsCommandNonInteractively(t *testing.T) {
	runner := &fakeSSHRunner{}
	connector := MachineConnector{Runner: runner}
	err := connector.ConnectMachine(context.Background(), machine.ConnectPlan{
		Tenant:       tenant.Summary{IncusName: "sc-acme"},
		Project:      "default",
		InstanceName: "default-codex",
		SSHHost:      "10.248.0.20",
		HostKeyAlias: "codex.default.acme",
		Command:      []string{"pwd"},
		LinuxUser:    "alice",
		WorkingDir:   "/workspace",
		Interactive:  false,
		Managed:      true,
	}, machine.ConnectSession{
		Stdin:  io.Reader(nil),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.args, " ")
	if strings.Contains(joined, " -t ") {
		t.Fatalf("non-interactive ssh should not force tty: %q", joined)
	}
	if !strings.Contains(joined, "alice@10.248.0.20") || !strings.Contains(joined, "cd /workspace && exec /bin/bash -lc pwd") {
		t.Fatalf("ssh args = %q", joined)
	}
}

func TestMachineConnectorSSHsSingleStringCommandThroughRemoteShell(t *testing.T) {
	runner := &fakeSSHRunner{}
	connector := MachineConnector{Runner: runner}
	err := connector.ConnectMachine(context.Background(), machine.ConnectPlan{
		Tenant:       tenant.Summary{IncusName: "sc-acme"},
		Project:      "default",
		InstanceName: "default-codex",
		SSHHost:      "10.248.0.20",
		HostKeyAlias: "codex.default.acme",
		Command:      []string{"touch hase"},
		LinuxUser:    "alice",
		WorkingDir:   "/workspace",
		Managed:      true,
	}, machine.ConnectSession{
		Stdin:  io.Reader(nil),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.args, " ")
	if !strings.Contains(joined, `cd /workspace && exec /bin/bash -lc 'touch hase'`) {
		t.Fatalf("ssh args = %q", joined)
	}
	if strings.Contains(joined, "exec 'touch hase'") {
		t.Fatalf("single string command should not be quoted as executable: %q", joined)
	}
}
