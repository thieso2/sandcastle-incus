package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	"github.com/thieso2/sandcastle-incus/internal/hostoverride"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
	"github.com/thieso2/sandcastle-incus/internal/tailscale"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

func executeForTest(t *testing.T, name string, args ...string) (string, error) {
	return executeForTestWithConfig(t, commandConfig{name: name}, args...)
}

func executeForTestWithConfig(t *testing.T, config commandConfig, args ...string) (string, error) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	config.stdout = &stdout
	config.stderr = &stderr
	cmd := NewRootCommand(config)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	if stderr.Len() > 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	return stdout.String(), err
}

func TestVersionText(t *testing.T) {
	stdout, err := executeForTest(t, "sandcastle", "version")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(stdout); got != version {
		t.Fatalf("version output = %q, want %q", got, version)
	}
}

func TestVersionJSONUsesBinaryName(t *testing.T) {
	stdout, err := executeForTest(t, "sc", "--output", "json", "version")
	if err != nil {
		t.Fatal(err)
	}
	var payload versionPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Name != "sc" {
		t.Fatalf("payload.Name = %q, want sc", payload.Name)
	}
	if payload.Version != version {
		t.Fatalf("payload.Version = %q, want %q", payload.Version, version)
	}
}

func TestListJSONStartsEmpty(t *testing.T) {
	stdout, err := executeForTest(t, "sandcastle", "--output", "json", "ls")
	if err != nil {
		t.Fatal(err)
	}
	var payload listPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Projects) != 0 {
		t.Fatalf("len(payload.Projects) = %d, want 0", len(payload.Projects))
	}
}

func TestListTextShowsManagedProjects(t *testing.T) {
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
	}, "ls")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "alice/myproject") {
		t.Fatalf("stdout = %q, want project reference", stdout)
	}
	if !strings.Contains(stdout, "myproject.project-tld") {
		t.Fatalf("stdout = %q, want domain", stdout)
	}
}

func TestStatusJSON(t *testing.T) {
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
	}, "--output", "json", "status", "alice/myproject")
	if err != nil {
		t.Fatal(err)
	}
	var payload project.Status
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Summary.IncusName != "sc-alice-myproject" {
		t.Fatalf("IncusName = %q", payload.Summary.IncusName)
	}
}

func TestAddDryRunJSON(t *testing.T) {
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
	}, "--output", "json", "add", "alice/myproject/codex", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload sandbox.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.PrivateIP != "10.248.0.20" {
		t.Fatalf("PrivateIP = %q", payload.PrivateIP)
	}
}

func TestAddDetachSkipsEnter(t *testing.T) {
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	creator := &fakeSandboxCreator{}
	enterer := &fakeSandboxEnterer{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
		sandboxCreator: creator,
		sandboxEnterer: enterer,
	}, "add", "alice/myproject/codex", "--detach")
	if err != nil {
		t.Fatal(err)
	}
	if creator.plan.InstanceName != "sc-codex" {
		t.Fatalf("created instance = %q", creator.plan.InstanceName)
	}
	if enterer.called {
		t.Fatal("expected add --detach to skip enter")
	}
}

func TestAddEntersAfterCreateByDefault(t *testing.T) {
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	creator := &fakeSandboxCreator{}
	enterer := &fakeSandboxEnterer{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
		sandboxCreator: creator,
		sandboxEnterer: enterer,
	}, "add", "alice/myproject/codex")
	if err != nil {
		t.Fatal(err)
	}
	if creator.plan.InstanceName != "sc-codex" {
		t.Fatalf("created instance = %q", creator.plan.InstanceName)
	}
	if !enterer.called {
		t.Fatal("expected add to enter sandbox")
	}
	if enterer.plan.InstanceName != "sc-codex" {
		t.Fatalf("entered instance = %q", enterer.plan.InstanceName)
	}
}

func TestEnterCommandUsesEnterer(t *testing.T) {
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	enterer := &fakeSandboxEnterer{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
		sandboxEnterer: enterer,
	}, "enter", "alice/myproject/codex")
	if err != nil {
		t.Fatal(err)
	}
	if !enterer.called {
		t.Fatal("expected enterer call")
	}
	if enterer.plan.InstanceName != "sc-codex" {
		t.Fatalf("entered instance = %q", enterer.plan.InstanceName)
	}
}

func TestRemoveRequiresConfirmation(t *testing.T) {
	_, err := executeForTest(t, "sandcastle", "rm", "alice/myproject/codex")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %q, want --yes hint", err.Error())
	}
}

func TestPortSetRejectsInvalidPort(t *testing.T) {
	_, err := executeForTest(t, "sandcastle", "port", "set", "alice/myproject/codex", "bad")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDNSStatusJSON(t *testing.T) {
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
	}, "--output", "json", "dns", "status", "alice/myproject")
	if err != nil {
		t.Fatal(err)
	}
	var payload dns.ApplyResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.DNSAddress != "10.248.0.53" {
		t.Fatalf("DNSAddress = %q", payload.DNSAddress)
	}
}

func TestTailscaleUpDryRunRedactsAuthKey(t *testing.T) {
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
	}, "--output", "json", "tailscale", "up", "alice/myproject", "--auth-key", "tskey-secret", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "tskey-secret") {
		t.Fatalf("stdout leaked auth key: %s", stdout)
	}
	var payload tailscale.UpPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.InstanceName != project.TailscaleName {
		t.Fatalf("InstanceName = %q", payload.InstanceName)
	}
	if !payload.HasAuthKey {
		t.Fatal("expected HasAuthKey")
	}
}

func TestTailscaleUpRunsExecutor(t *testing.T) {
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeTailscaleRunner{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
		tailscale: runner,
	}, "tailscale", "up", "alice/myproject", "--auth-key", "tskey-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !runner.called {
		t.Fatal("expected tailscale runner call")
	}
	if runner.plan.InstanceName != project.TailscaleName {
		t.Fatalf("InstanceName = %q", runner.plan.InstanceName)
	}
	if runner.plan.AuthKey != "tskey-secret" {
		t.Fatalf("AuthKey = %q", runner.plan.AuthKey)
	}
}

func TestTailscaleStatusRunsExecutor(t *testing.T) {
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeTailscaleRunner{status: tailscale.StatusResult{
		Reference: "alice/myproject",
		Tailscale: meta.Tailscale{State: "Running", TailscaleIPs: []string{"100.80.12.34"}},
	}}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
		tailscale: runner,
	}, "--output", "json", "tailscale", "status", "alice/myproject")
	if err != nil {
		t.Fatal(err)
	}
	if !runner.statusCalled {
		t.Fatal("expected tailscale status runner call")
	}
	var payload tailscale.StatusResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Tailscale.State != "Running" {
		t.Fatalf("State = %q", payload.Tailscale.State)
	}
}

func TestTailscaleDownDryRunJSON(t *testing.T) {
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
	}, "--output", "json", "tailscale", "down", "alice/myproject", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload tailscale.DownPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Join(payload.Command, " ") != "tailscale down" {
		t.Fatalf("Command = %#v", payload.Command)
	}
}

func TestHostOverrideAddDryRunJSON(t *testing.T) {
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
		hostSandbox: fakeHostSandboxStore{},
	}, "--output", "json", "host", "override", "add", "alice/myproject/codex", "Example.COM", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload hostoverride.AddPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Hostname != "example.com" {
		t.Fatalf("Hostname = %q", payload.Hostname)
	}
	if payload.IPAddress != "10.248.0.20" {
		t.Fatalf("IPAddress = %q", payload.IPAddress)
	}
}

func TestAdminVersion(t *testing.T) {
	stdout, err := executeForTest(t, "sandcastle", "admin", "version")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(stdout); got != version {
		t.Fatalf("admin version output = %q, want %q", got, version)
	}
}

func TestAdminProjectListJSON(t *testing.T) {
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
	}, "--output", "json", "admin", "project", "list")
	if err != nil {
		t.Fatal(err)
	}
	var payload listPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Projects) != 1 {
		t.Fatalf("len(payload.Projects) = %d, want 1", len(payload.Projects))
	}
	if payload.Projects[0].IncusName != "sc-alice-myproject" {
		t.Fatalf("IncusName = %q", payload.Projects[0].IncusName)
	}
}

func TestAdminProjectCreateDryRunJSON(t *testing.T) {
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "admin", "project", "create", "alice/myproject", "--domain", "myproject.project-tld", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload project.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.IncusProject != "sc-alice-myproject" {
		t.Fatalf("IncusProject = %q", payload.IncusProject)
	}
	if payload.PrivateCIDR != "10.248.0.0/24" {
		t.Fatalf("PrivateCIDR = %q", payload.PrivateCIDR)
	}
}

func TestAdminProjectCreateRequiresExecutor(t *testing.T) {
	_, err := executeForTest(t, "sandcastle", "admin", "project", "create", "alice/myproject", "--domain", "myproject.project-tld")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "executor") {
		t.Fatalf("error = %q, want executor hint", err.Error())
	}
}

func TestAdminProjectDeleteRequiresConfirmation(t *testing.T) {
	_, err := executeForTest(t, "sandcastle", "admin", "project", "delete", "alice/myproject")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %q, want --yes hint", err.Error())
	}
}

func TestAdminUserGrantDryRunJSON(t *testing.T) {
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "admin", "user", "grant", "alice", "alice/myproject", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload usertrust.UserPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.CertificateName != "sandcastle-alice" {
		t.Fatalf("CertificateName = %q", payload.CertificateName)
	}
	if len(payload.Projects) != 1 || payload.Projects[0] != "sc-alice-myproject" {
		t.Fatalf("Projects = %#v", payload.Projects)
	}
}

func TestRejectsUnknownOutputFormat(t *testing.T) {
	_, err := executeForTest(t, "sandcastle", "--output", "yaml", "version")
	if err == nil {
		t.Fatal("expected error")
	}
}

type fakeSandboxCreator struct {
	plan sandbox.CreatePlan
}

func (f *fakeSandboxCreator) CreateSandbox(ctx context.Context, plan sandbox.CreatePlan) error {
	f.plan = plan
	return nil
}

type fakeSandboxEnterer struct {
	called bool
	plan   sandbox.EnterPlan
}

func (f *fakeSandboxEnterer) EnterSandbox(ctx context.Context, plan sandbox.EnterPlan, session sandbox.EnterSession) error {
	f.called = true
	f.plan = plan
	return nil
}

type fakeTailscaleRunner struct {
	called       bool
	statusCalled bool
	downCalled   bool
	plan         tailscale.UpPlan
	status       tailscale.StatusResult
}

func (f *fakeTailscaleRunner) RunUp(ctx context.Context, plan tailscale.UpPlan, session tailscale.RunSession) error {
	f.called = true
	f.plan = plan
	return nil
}

func (f *fakeTailscaleRunner) RunStatus(ctx context.Context, plan tailscale.StatusPlan, session tailscale.RunSession) (tailscale.StatusResult, error) {
	f.statusCalled = true
	return f.status, nil
}

func (f *fakeTailscaleRunner) RunDown(ctx context.Context, plan tailscale.DownPlan, session tailscale.RunSession) error {
	f.downCalled = true
	return nil
}

type fakeHostSandboxStore struct{}

func (f fakeHostSandboxStore) FindSandbox(ctx context.Context, summary project.Summary, name string) (meta.Sandbox, error) {
	return meta.Sandbox{
		Owner:     summary.Owner,
		Project:   summary.Name,
		Name:      name,
		AppPort:   3000,
		PrivateIP: "10.248.0.20",
	}, nil
}
