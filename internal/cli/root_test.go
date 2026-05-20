package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	"github.com/thieso2/sandcastle-incus/internal/domain"
	"github.com/thieso2/sandcastle-incus/internal/hostoverride"
	"github.com/thieso2/sandcastle-incus/internal/images"
	"github.com/thieso2/sandcastle-incus/internal/infra"
	"github.com/thieso2/sandcastle-incus/internal/localdns"
	"github.com/thieso2/sandcastle-incus/internal/localtrust"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/route"
	"github.com/thieso2/sandcastle-incus/internal/routebroker"
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

func TestStatusJSONSupportsProjectShorthandWithOwner(t *testing.T) {
	t.Setenv("SANDCASTLE_OWNER", "alice")
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
	}, "--output", "json", "status", "myproject")
	if err != nil {
		t.Fatal(err)
	}
	var payload project.Status
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Summary.Owner != "alice" || payload.Summary.Name != "myproject" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestInspectJSON(t *testing.T) {
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
		sandboxStore: fakeSandboxInspectStore{sandboxes: []meta.Sandbox{{
			Owner:        "alice",
			Project:      "myproject",
			Name:         "codex",
			AppPort:      5173,
			PrivateIP:    "10.248.0.20",
			LinuxUser:    "alice",
			HomeDir:      ".",
			WorkspaceDir: "workspace",
			Running:      true,
		}}},
	}, "--output", "json", "inspect", "alice/myproject/codex")
	if err != nil {
		t.Fatal(err)
	}
	var payload sandbox.InspectResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.InstanceName != "sc-codex" {
		t.Fatalf("InstanceName = %q", payload.InstanceName)
	}
	if payload.Sandbox.AppPort != 5173 || payload.Sandbox.LinuxUser != "alice" || !payload.Sandbox.Running {
		t.Fatalf("Sandbox = %#v", payload.Sandbox)
	}
}

func TestInspectText(t *testing.T) {
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
		sandboxStore: fakeSandboxInspectStore{sandboxes: []meta.Sandbox{{
			Owner:        "alice",
			Project:      "myproject",
			Name:         "codex",
			AppPort:      5173,
			PrivateIP:    "10.248.0.20",
			LinuxUser:    "alice",
			HomeDir:      ".",
			WorkspaceDir: "workspace",
			ExtraSANs:    []string{"app.example.com"},
		}}},
	}, "inspect", "alice/myproject/codex")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Sandbox: alice/myproject/codex", "Instance: sc-codex", "Private IP: 10.248.0.20", "Linux user: alice", "Extra SANs: app.example.com"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
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
	if payload.Template != "ai" {
		t.Fatalf("Template = %q", payload.Template)
	}
	if payload.HomeDir != "." || payload.WorkspaceDir != "." {
		t.Fatalf("HomeDir/WorkspaceDir = %q/%q, want ./.", payload.HomeDir, payload.WorkspaceDir)
	}
	if payload.LinuxUser != "alice" {
		t.Fatalf("LinuxUser = %q", payload.LinuxUser)
	}
}

func TestAddDryRunSupportsProjectNameShorthandWithOwner(t *testing.T) {
	t.Setenv("SANDCASTLE_OWNER", "alice")
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
	}, "--output", "json", "add", "myproject/codex", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload sandbox.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Project.Owner != "alice" || payload.Project.Name != "myproject" || payload.Name != "codex" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.LinuxUser != "alice" {
		t.Fatalf("LinuxUser = %q", payload.LinuxUser)
	}
}

func TestAddDryRunSupportsTemplateAndStorageFlags(t *testing.T) {
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
	}, "--output", "json", "add", "alice/myproject/minimal", "--dry-run", "--template", "base", "--home-dir", "shared-home", "--workspace-dir", ".")
	if err != nil {
		t.Fatal(err)
	}
	var payload sandbox.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Template != "base" {
		t.Fatalf("Template = %q", payload.Template)
	}
	if payload.ImageAlias != scconfig.DefaultBaseImageAlias {
		t.Fatalf("ImageAlias = %q", payload.ImageAlias)
	}
	if payload.HomeDir != "shared-home" || payload.WorkspaceDir != "." {
		t.Fatalf("HomeDir/WorkspaceDir = %q/%q", payload.HomeDir, payload.WorkspaceDir)
	}
}

func TestAddDryRunRejectsUnsafeStorageFlags(t *testing.T) {
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
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
	}, "add", "alice/myproject/minimal", "--dry-run", "--home-dir", "../shared")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "must not contain .. path segments") {
		t.Fatalf("error = %q", err)
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

func TestAddBackgroundSkipsEnter(t *testing.T) {
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
	}, "add", "alice/myproject/codex", "--background")
	if err != nil {
		t.Fatal(err)
	}
	if creator.plan.InstanceName != "sc-codex" {
		t.Fatalf("created instance = %q", creator.plan.InstanceName)
	}
	if enterer.called {
		t.Fatal("expected add --background to skip enter")
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
	if !enterer.plan.Interactive {
		t.Fatal("expected default enter to be interactive")
	}
}

func TestEnterCommandAcceptsExplicitCommand(t *testing.T) {
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
	}, "enter", "alice/myproject/codex", "pwd")
	if err != nil {
		t.Fatal(err)
	}
	if enterer.plan.Interactive {
		t.Fatal("expected explicit enter command to be non-interactive")
	}
	if len(enterer.plan.Command) != 1 || enterer.plan.Command[0] != "pwd" {
		t.Fatalf("Command = %#v", enterer.plan.Command)
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

func TestDNSInstallDryRunJSON(t *testing.T) {
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
	}, "--output", "json", "dns", "install", "alice/myproject", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload localdns.Plan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.DNSEndpoint != "10.248.0.53:53" {
		t.Fatalf("DNSEndpoint = %q", payload.DNSEndpoint)
	}
}

func TestDNSRefreshRunsLocalDNSExecutor(t *testing.T) {
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
	manager := &fakeLocalDNSManager{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
		localDNS: manager,
	}, "dns", "refresh", "alice/myproject")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.refreshed {
		t.Fatal("expected local DNS refresh call")
	}
	if manager.plan.DNSEndpoint != "10.248.0.53:53" {
		t.Fatalf("DNSEndpoint = %q", manager.plan.DNSEndpoint)
	}
}

func TestDNSServiceInstallDryRunJSON(t *testing.T) {
	t.Setenv("SANDCASTLE_BIN", "/usr/local/bin/sandcastle")
	t.Setenv("SANDCASTLE_LOCAL_DNS_STATE", filepath.Join(t.TempDir(), "dns.yaml"))
	t.Setenv("SANDCASTLE_LOCAL_DNS_SERVICE_DIR", t.TempDir())
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
	}, "--output", "json", "dns", "service", "install", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload localdns.ServicePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Action != "install" {
		t.Fatalf("Action = %q", payload.Action)
	}
	if payload.Executable != "/usr/local/bin/sandcastle" {
		t.Fatalf("Executable = %q", payload.Executable)
	}
	if !strings.Contains(payload.Content, "forwarder") {
		t.Fatalf("Content = %q", payload.Content)
	}
}

func TestDNSServiceReloadRunsExecutor(t *testing.T) {
	t.Setenv("SANDCASTLE_BIN", "/usr/local/bin/sandcastle")
	t.Setenv("SANDCASTLE_LOCAL_DNS_STATE", filepath.Join(t.TempDir(), "dns.yaml"))
	t.Setenv("SANDCASTLE_LOCAL_DNS_SERVICE_DIR", t.TempDir())
	manager := &fakeLocalDNSServiceManager{}
	_, err := executeForTestWithConfig(t, commandConfig{
		name:            "sandcastle",
		localDNSService: manager,
	}, "dns", "service", "reload")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.reloaded {
		t.Fatal("expected local DNS service reload call")
	}
	if manager.plan.Action != "reload" {
		t.Fatalf("Action = %q", manager.plan.Action)
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

func TestTailscaleUpDryRunUsesDefaultAdvertiseTag(t *testing.T) {
	t.Setenv("SANDCASTLE_E2E_TAILSCALE_TAG", "")
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
	}, "--output", "json", "tailscale", "up", "alice/myproject", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload tailscale.UpPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.AdvertiseTags) != 1 || payload.AdvertiseTags[0] != tailscale.DefaultAdvertiseTag {
		t.Fatalf("AdvertiseTags = %#v", payload.AdvertiseTags)
	}
	if !strings.Contains(strings.Join(payload.Command, " "), "--advertise-tags="+tailscale.DefaultAdvertiseTag) {
		t.Fatalf("Command = %#v", payload.Command)
	}
}

func TestTailscaleUpDryRunRejectsInvalidAdvertiseTag(t *testing.T) {
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
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
	}, "tailscale", "up", "alice/myproject", "--advertise-tag", "sandcastle", "--dry-run")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Tailscale advertise tag") {
		t.Fatalf("error = %q", err)
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
	t.Setenv("SANDCASTLE_OWNER", "alice")
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
	}, "--output", "json", "host", "override", "add", "myproject/codex", "Example.COM", "--dry-run")
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

func TestHostOverrideAddAppliesSandboxAndHosts(t *testing.T) {
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
	manager := &fakeHostOverrideManager{}
	files := &fakeHostFiles{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
		hostSandbox:   fakeHostSandboxStore{},
		hostOverrides: manager,
		hostFiles:     files,
	}, "host", "override", "add", "alice/myproject/codex", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.called {
		t.Fatal("expected host override manager call")
	}
	if !files.called {
		t.Fatal("expected hosts file editor call")
	}
	if files.plan.Hostname != "example.com" {
		t.Fatalf("Hostname = %q", files.plan.Hostname)
	}
}

func TestHostOverrideListJSON(t *testing.T) {
	t.Setenv("SANDCASTLE_OWNER", "alice")
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
	}, "--output", "json", "host", "override", "list", "myproject")
	if err != nil {
		t.Fatal(err)
	}
	var payload hostoverride.ListResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Overrides) != 1 || payload.Overrides[0].Hostname != "example.com" {
		t.Fatalf("Overrides = %#v", payload.Overrides)
	}
}

func TestHostOverrideRemoveAppliesSandboxAndHosts(t *testing.T) {
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
	manager := &fakeHostOverrideManager{}
	files := &fakeHostFiles{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
		hostSandbox:   fakeHostSandboxStore{},
		hostOverrides: manager,
		hostFiles:     files,
	}, "host", "override", "rm", "alice/myproject/codex", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.removed {
		t.Fatal("expected host override remove call")
	}
	if !files.removed {
		t.Fatal("expected hosts file remove call")
	}
}

func TestTrustInstallDryRunJSON(t *testing.T) {
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
	}, "--output", "json", "trust", "install", "alice/myproject", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload localtrust.Plan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.CAVolume != project.CAVolumeName {
		t.Fatalf("CAVolume = %q", payload.CAVolume)
	}
	if !strings.Contains(payload.Warning, "mint certificates") {
		t.Fatalf("Warning = %q", payload.Warning)
	}
}

func TestTrustInstallRunsExecutor(t *testing.T) {
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
	manager := &fakeLocalTrustManager{}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
		localTrust: manager,
	}, "trust", "install", "alice/myproject")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.installed {
		t.Fatal("expected local trust install call")
	}
	if !strings.Contains(stdout, "Warning: Trusting this project CA") {
		t.Fatalf("stdout missing pre-install trust warning: %q", stdout)
	}
	if !strings.Contains(stdout, "install project CA trust: alice/myproject") {
		t.Fatalf("stdout missing trust result: %q", stdout)
	}
	if manager.plan.IncusProject != "sc-alice-myproject" {
		t.Fatalf("IncusProject = %q", manager.plan.IncusProject)
	}
}

func TestTrustUninstallRunsExecutor(t *testing.T) {
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
	manager := &fakeLocalTrustManager{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
		localTrust: manager,
	}, "trust", "uninstall", "alice/myproject")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.removed {
		t.Fatal("expected local trust uninstall call")
	}
}

func TestRouteAddDryRunJSON(t *testing.T) {
	t.Setenv("SANDCASTLE_OWNER", "alice")
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
		name:        "sandcastle",
		adminConfig: routeAdminConfigForTest(),
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
		routeSandbox: fakeRouteSandboxStore{},
	}, "--output", "json", "route", "add", "App.Example.COM", "myproject/codex", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload route.AddPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Hostname != "app.example.com" {
		t.Fatalf("Hostname = %q", payload.Hostname)
	}
	if payload.RoutePort != 5173 {
		t.Fatalf("RoutePort = %d", payload.RoutePort)
	}
}

func TestRouteAddRequiresBrokerExecutor(t *testing.T) {
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
	_, err = executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: routeAdminConfigForTest(),
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
		routeSandbox: fakeRouteSandboxStore{},
	}, "route", "add", "app.example.com", "alice/myproject/codex")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "route broker") {
		t.Fatalf("error = %q", err.Error())
	}
}

func routeAdminConfigForTest() scconfig.Admin {
	admin := scconfig.LoadAdminFromEnv()
	admin.InfrastructureHost = "203.0.113.10"
	return admin
}

func TestRouteManagerFromEnvUsesBrokerClient(t *testing.T) {
	t.Setenv("SANDCASTLE_ROUTE_BROKER_URL", "https://broker.example.com")
	t.Setenv("SANDCASTLE_ROUTE_BROKER_CLIENT_CERT", "/tmp/client.crt")
	t.Setenv("SANDCASTLE_ROUTE_BROKER_CLIENT_KEY", "/tmp/client.key")
	t.Setenv("SANDCASTLE_ROUTE_BROKER_INSECURE_SKIP_VERIFY", "1")

	manager := routeManagerFromEnv(&fakeRouteManager{})
	client, ok := manager.(routebroker.Client)
	if !ok {
		t.Fatalf("manager = %T, want routebroker.Client", manager)
	}
	if client.BaseURL != "https://broker.example.com" || client.CertFile != "/tmp/client.crt" || client.KeyFile != "/tmp/client.key" {
		t.Fatalf("client = %#v", client)
	}
	if !client.InsecureSkipVerify {
		t.Fatal("expected insecure skip verify flag")
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

func TestAdminProjectCreateRejectsDuplicateDomainForSameOwner(t *testing.T) {
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "shared.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	creator := &fakeProjectCreator{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		projectStore: project.MemoryStore{Projects: []project.IncusProject{{
			Name:   "sc-alice-myproject",
			Config: configMap,
		}}},
		projectCreator: creator,
	}, "admin", "project", "create", "alice/other", "--domain", "shared.project-tld")
	if err == nil {
		t.Fatal("expected duplicate domain error")
	}
	if !strings.Contains(err.Error(), "already used") {
		t.Fatalf("error = %q", err.Error())
	}
	if creator.called {
		t.Fatal("creator should not be called for duplicate domain")
	}
}

func TestAdminTLDRefreshWritesSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# Version 2026050700\nCOM\nORG\n"))
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "tld_snapshot_generated.go")
	stdout, err := executeForTest(t, "sandcastle", "admin", "tld", "refresh", "--source-url", server.URL, "--output-file", output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Refreshed 2 public TLDs") {
		t.Fatalf("stdout = %q", stdout)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"com": true`) {
		t.Fatalf("content = %s", string(content))
	}
}

func TestAdminTLDRefreshDryRunJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("COM\nORG\n"))
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "tld_snapshot_generated.go")
	stdout, err := executeForTest(t, "sandcastle", "--output", "json", "admin", "tld", "refresh", "--source-url", server.URL, "--output-file", output, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload domain.RefreshResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 2 || payload.Written {
		t.Fatalf("payload = %#v", payload)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("expected dry run not to write output, stat err = %v", err)
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

func TestAdminInfraCreateDryRunJSON(t *testing.T) {
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "admin", "infra", "create", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload infra.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Project != scconfig.DefaultInfrastructureProject {
		t.Fatalf("Project = %q", payload.Project)
	}
	if payload.CaddyInstance != "sc-caddy" {
		t.Fatalf("CaddyInstance = %q", payload.CaddyInstance)
	}
	if payload.RouteBrokerInstance != infra.RouteBrokerName {
		t.Fatalf("RouteBrokerInstance = %q", payload.RouteBrokerInstance)
	}
}

func TestAdminInfraCreateCallsExecutor(t *testing.T) {
	creator := &fakeInfraCreator{}
	_, err := executeForTestWithConfig(t, commandConfig{
		name:         "sandcastle",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		infraCreator: creator,
	}, "admin", "infra", "create")
	if err != nil {
		t.Fatal(err)
	}
	if !creator.called {
		t.Fatal("expected infrastructure creator to be called")
	}
	if creator.plan.Project != scconfig.DefaultInfrastructureProject {
		t.Fatalf("Project = %q", creator.plan.Project)
	}
}

func TestAdminInfraCreateRequiresExecutor(t *testing.T) {
	_, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "admin", "infra", "create")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "infrastructure creation executor") {
		t.Fatalf("error = %q", err)
	}
}

func TestAdminInfraDeleteRequiresConfirmation(t *testing.T) {
	_, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "admin", "infra", "delete")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %q", err)
	}
}

func TestAdminInfraDeleteCallsExecutor(t *testing.T) {
	deleter := &fakeInfraDeleter{}
	_, err := executeForTestWithConfig(t, commandConfig{
		name:         "sandcastle",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		infraDeleter: deleter,
	}, "admin", "infra", "delete", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !deleter.called {
		t.Fatal("expected infrastructure deleter to be called")
	}
	if deleter.plan.Project != scconfig.DefaultInfrastructureProject {
		t.Fatalf("Project = %q", deleter.plan.Project)
	}
}

func TestAdminImageSyncDryRunJSON(t *testing.T) {
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "admin", "image", "sync", "sandcastle/base:debian-13", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload images.SyncPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Template != "base" {
		t.Fatalf("Template = %q", payload.Template)
	}
	if payload.Alias != scconfig.DefaultBaseImageAlias {
		t.Fatalf("Alias = %q", payload.Alias)
	}
}

func TestAdminImageBuildDryRunJSON(t *testing.T) {
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "admin", "image", "build", "base", "--tag", "sandcastle/base:debian-13", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload images.BuildPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Template != "base" || payload.Tag != "sandcastle/base:debian-13" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestAdminImageBuildRequiresPinnedAIVersions(t *testing.T) {
	_, err := executeForTest(t, "sandcastle", "admin", "image", "build", "ai", "--dry-run")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "codex-version") {
		t.Fatalf("error = %q", err)
	}
}

func TestAdminImageBuildCallsExecutor(t *testing.T) {
	builder := &fakeImageBuilder{}
	_, err := executeForTestWithConfig(t, commandConfig{
		name:         "sandcastle",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		imageBuilder: builder,
	}, "admin", "image", "build", "base", "--tag", "sandcastle/base:debian-13")
	if err != nil {
		t.Fatal(err)
	}
	if !builder.called {
		t.Fatal("expected image builder to be called")
	}
	if builder.plan.Tag != "sandcastle/base:debian-13" {
		t.Fatalf("Tag = %q", builder.plan.Tag)
	}
}

func TestAdminImageImportDryRunJSON(t *testing.T) {
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "admin", "image", "import", "base", "oci:sandcastle/base:debian-13", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload images.ImportPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Alias != scconfig.DefaultBaseImageAlias {
		t.Fatalf("Alias = %q", payload.Alias)
	}
	if !strings.Contains(strings.Join(payload.Command, " "), "image copy oci:sandcastle/base:debian-13") {
		t.Fatalf("Command = %#v", payload.Command)
	}
}

func TestAdminImageImportCallsExecutor(t *testing.T) {
	importer := &fakeImageImporter{}
	_, err := executeForTestWithConfig(t, commandConfig{
		name:          "sandcastle",
		adminConfig:   scconfig.LoadAdminFromEnv(),
		imageImporter: importer,
	}, "admin", "image", "import", "ai", "oci:sandcastle/ai:debian-13")
	if err != nil {
		t.Fatal(err)
	}
	if !importer.called {
		t.Fatal("expected image importer to be called")
	}
	if importer.plan.Alias != scconfig.DefaultAIImageAlias {
		t.Fatalf("Alias = %q", importer.plan.Alias)
	}
}

func TestAdminImageSyncCallsExecutor(t *testing.T) {
	manager := &fakeImageManager{result: images.SyncResult{Fingerprint: "abc123", Action: "created"}}
	_, err := executeForTestWithConfig(t, commandConfig{
		name:         "sandcastle",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		imageManager: manager,
	}, "admin", "image", "sync", "sandcastle/ai:debian-13")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.called {
		t.Fatal("expected image manager to be called")
	}
	if manager.plan.Alias != scconfig.DefaultAIImageAlias {
		t.Fatalf("Alias = %q", manager.plan.Alias)
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

func TestAdminRouteBrokerServeCallsRunner(t *testing.T) {
	runner := &fakeRouteBrokerRunner{}
	_, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		routeBroker: runner,
	}, "admin", "route-broker", "serve", "--listen", "127.0.0.1:9443", "--cert", "/tmp/broker.crt", "--key", "/tmp/broker.key")
	if err != nil {
		t.Fatal(err)
	}
	if !runner.called {
		t.Fatal("expected route broker runner to be called")
	}
	if runner.plan.Address != "127.0.0.1:9443" {
		t.Fatalf("Address = %q", runner.plan.Address)
	}
	if runner.plan.CertFile != "/tmp/broker.crt" || runner.plan.KeyFile != "/tmp/broker.key" {
		t.Fatalf("plan = %#v", runner.plan)
	}
}

func TestAdminRouteBrokerServeRequiresConfiguredRunner(t *testing.T) {
	_, err := executeForTest(t, "sandcastle", "admin", "route-broker", "serve", "--cert", "/tmp/broker.crt", "--key", "/tmp/broker.key")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "route broker server") {
		t.Fatalf("error = %q", err)
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

type fakeProjectCreator struct {
	called bool
	plan   project.CreatePlan
}

func (f *fakeProjectCreator) CreateProject(ctx context.Context, plan project.CreatePlan) error {
	f.called = true
	f.plan = plan
	return nil
}

type fakeInfraCreator struct {
	called bool
	plan   infra.CreatePlan
}

func (f *fakeInfraCreator) CreateInfrastructure(ctx context.Context, plan infra.CreatePlan) error {
	f.called = true
	f.plan = plan
	return nil
}

type fakeInfraDeleter struct {
	called bool
	plan   infra.DeletePlan
}

func (f *fakeInfraDeleter) DeleteInfrastructure(ctx context.Context, plan infra.DeletePlan) error {
	f.called = true
	f.plan = plan
	return nil
}

type fakeImageManager struct {
	called bool
	plan   images.SyncPlan
	result images.SyncResult
}

func (f *fakeImageManager) SyncImage(ctx context.Context, plan images.SyncPlan) (images.SyncResult, error) {
	f.called = true
	f.plan = plan
	f.result.SyncPlan = plan
	return f.result, nil
}

type fakeImageBuilder struct {
	called bool
	plan   images.BuildPlan
}

func (f *fakeImageBuilder) BuildImage(ctx context.Context, plan images.BuildPlan) (images.BuildResult, error) {
	f.called = true
	f.plan = plan
	return images.BuildResult{BuildPlan: plan, Built: true}, nil
}

type fakeImageImporter struct {
	called bool
	plan   images.ImportPlan
}

func (f *fakeImageImporter) ImportImage(ctx context.Context, plan images.ImportPlan) (images.ImportResult, error) {
	f.called = true
	f.plan = plan
	return images.ImportResult{ImportPlan: plan, Imported: true}, nil
}

type fakeTailscaleRunner struct {
	called       bool
	statusCalled bool
	downCalled   bool
	plan         tailscale.UpPlan
	status       tailscale.StatusResult
}

type fakeLocalDNSManager struct {
	installed   bool
	refreshed   bool
	uninstalled bool
	plan        localdns.Plan
}

type fakeLocalDNSServiceManager struct {
	installed   bool
	reloaded    bool
	uninstalled bool
	plan        localdns.ServicePlan
}

func (f *fakeLocalDNSManager) Install(ctx context.Context, plan localdns.Plan) (localdns.Result, error) {
	f.installed = true
	f.plan = plan
	return localdns.Result{Reference: plan.Reference, Action: "install", StatePath: plan.StatePath, ResolverPath: plan.ResolverPath}, nil
}

func (f *fakeLocalDNSManager) Refresh(ctx context.Context, plan localdns.Plan) (localdns.Result, error) {
	f.refreshed = true
	f.plan = plan
	return localdns.Result{Reference: plan.Reference, Action: "refresh", StatePath: plan.StatePath, ResolverPath: plan.ResolverPath}, nil
}

func (f *fakeLocalDNSManager) Uninstall(ctx context.Context, plan localdns.Plan) (localdns.Result, error) {
	f.uninstalled = true
	f.plan = plan
	return localdns.Result{Reference: plan.Reference, Action: "uninstall", StatePath: plan.StatePath, ResolverPath: plan.ResolverPath}, nil
}

func (f *fakeLocalDNSServiceManager) InstallService(ctx context.Context, plan localdns.ServicePlan) (localdns.ServiceResult, error) {
	f.installed = true
	f.plan = plan
	return localdns.ServiceResult{Action: plan.Action, Strategy: plan.Strategy, ServicePath: plan.ServicePath}, nil
}

func (f *fakeLocalDNSServiceManager) ReloadService(ctx context.Context, plan localdns.ServicePlan) (localdns.ServiceResult, error) {
	f.reloaded = true
	f.plan = plan
	return localdns.ServiceResult{Action: plan.Action, Strategy: plan.Strategy, ServicePath: plan.ServicePath}, nil
}

func (f *fakeLocalDNSServiceManager) UninstallService(ctx context.Context, plan localdns.ServicePlan) (localdns.ServiceResult, error) {
	f.uninstalled = true
	f.plan = plan
	return localdns.ServiceResult{Action: plan.Action, Strategy: plan.Strategy, ServicePath: plan.ServicePath}, nil
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
		ExtraSANs: []string{"example.com"},
	}, nil
}

func (f fakeHostSandboxStore) ListSandboxes(ctx context.Context, summary project.Summary) ([]meta.Sandbox, error) {
	sandbox, err := f.FindSandbox(ctx, summary, "codex")
	if err != nil {
		return nil, err
	}
	return []meta.Sandbox{sandbox}, nil
}

type fakeSandboxInspectStore struct {
	sandboxes []meta.Sandbox
}

func (f fakeSandboxInspectStore) ListSandboxes(ctx context.Context, summary project.Summary) ([]meta.Sandbox, error) {
	return f.sandboxes, nil
}

type fakeHostOverrideManager struct {
	called  bool
	removed bool
	plan    hostoverride.AddPlan
}

func (f *fakeHostOverrideManager) Add(ctx context.Context, plan hostoverride.AddPlan) error {
	f.called = true
	f.plan = plan
	return nil
}

func (f *fakeHostOverrideManager) Remove(ctx context.Context, plan hostoverride.RemovePlan) error {
	f.removed = true
	return nil
}

type fakeRouteSandboxStore struct{}

func (f fakeRouteSandboxStore) FindSandbox(ctx context.Context, summary project.Summary, name string) (meta.Sandbox, error) {
	return meta.Sandbox{
		Owner:     summary.Owner,
		Project:   summary.Name,
		Name:      name,
		AppPort:   5173,
		PrivateIP: "10.248.0.20",
	}, nil
}

type fakeRouteManager struct{}

func (f *fakeRouteManager) Add(ctx context.Context, plan route.AddPlan) error {
	return nil
}

func (f *fakeRouteManager) Remove(ctx context.Context, plan route.RemovePlan) error {
	return nil
}

func (f *fakeRouteManager) List(ctx context.Context, plan route.ListPlan) (route.ListResult, error) {
	return route.ListResult{}, nil
}

type fakeRouteBrokerRunner struct {
	called bool
	plan   routebroker.ServePlan
}

func (f *fakeRouteBrokerRunner) Serve(ctx context.Context, plan routebroker.ServePlan) error {
	f.called = true
	f.plan = plan
	return nil
}

type fakeHostFiles struct {
	called  bool
	removed bool
	plan    hostoverride.AddPlan
}

func (f *fakeHostFiles) AddHostsEntry(ctx context.Context, plan hostoverride.AddPlan) error {
	f.called = true
	f.plan = plan
	return nil
}

func (f *fakeHostFiles) RemoveHostsEntry(ctx context.Context, plan hostoverride.RemovePlan) error {
	f.removed = true
	return nil
}

type fakeLocalTrustManager struct {
	installed bool
	removed   bool
	plan      localtrust.Plan
}

func (f *fakeLocalTrustManager) Install(ctx context.Context, plan localtrust.Plan) (localtrust.Result, error) {
	f.installed = true
	f.plan = plan
	return localtrust.Result{Reference: plan.Reference, TrustName: plan.TrustName, Action: "install", Platform: "fake"}, nil
}

func (f *fakeLocalTrustManager) Uninstall(ctx context.Context, plan localtrust.Plan) (localtrust.Result, error) {
	f.removed = true
	f.plan = plan
	return localtrust.Result{Reference: plan.Reference, TrustName: plan.TrustName, Action: "uninstall", Platform: "fake"}, nil
}
