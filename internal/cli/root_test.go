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
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/route"
	"github.com/thieso2/sandcastle-incus/internal/routebroker"
	"github.com/thieso2/sandcastle-incus/internal/tailscale"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
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
	if config.adminConfig.Remote == "" {
		config.adminConfig = testAdminConfig()
	}
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

func executeAdminForTest(t *testing.T, name string, args ...string) (string, error) {
	return executeAdminForTestWithConfig(t, commandConfig{name: name}, args...)
}

func executeAdminForTestWithConfig(t *testing.T, config commandConfig, args ...string) (string, error) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	config.stdout = &stdout
	config.stderr = &stderr
	if config.adminConfig.Remote == "" {
		config.adminConfig = testAdminConfig()
	}
	cmd := NewAdminRootCommand(config)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	if stderr.Len() > 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	return stdout.String(), err
}

func testAdminConfig() scconfig.Admin {
	admin := scconfig.LoadAdminFromEnv()
	if admin.Tenant == "" {
		admin.Tenant = "acme"
	}
	return admin
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

func TestJSONFlagUsesJSONOutput(t *testing.T) {
	stdout, err := executeForTest(t, "sandcastle", "--json", "version")
	if err != nil {
		t.Fatal(err)
	}
	var payload versionPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Name != "sandcastle" {
		t.Fatalf("payload.Name = %q, want sandcastle", payload.Name)
	}
}

func TestJSONFlagRejectsExplicitTextOutput(t *testing.T) {
	_, err := executeForTest(t, "sandcastle", "--json", "--output", "text", "version")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--json") {
		t.Fatalf("error = %q", err)
	}
}

func TestConfigUnsetClearsStoredValue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configPath := scconfig.DefaultConfigPath()
	if err := scconfig.SaveSandcastleConfig(configPath, scconfig.SandcastleConfig{
		Tenant:      "acme",
		Project:     "website",
		Remote:      "sc-acme",
		AdminRemote: "big",
	}); err != nil {
		t.Fatal(err)
	}

	stdout, err := executeForTest(t, "sandcastle", "config", "unset", "project")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Unset project") {
		t.Fatalf("stdout = %q", stdout)
	}
	cfg, err := scconfig.LoadSandcastleConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project != "" {
		t.Fatalf("Project = %q, want empty", cfg.Project)
	}
	if cfg.Tenant != "acme" || cfg.Remote != "sc-acme" || cfg.AdminRemote != "big" {
		t.Fatalf("config = %#v, want other keys preserved", cfg)
	}
}

func TestConfigUnsetRejectsUnknownKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := executeForTest(t, "sandcastle", "config", "unset", "bad")
	if err == nil || !strings.Contains(err.Error(), "supported keys: tenant, project, remote, admin_remote") {
		t.Fatalf("err = %v", err)
	}
}

func TestListJSONStartsEmpty(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{},
	}, "--output", "json", "list")
	if err != nil {
		t.Fatal(err)
	}
	var payload listPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Machines) != 0 {
		t.Fatalf("len(payload.Machines) = %d, want 0", len(payload.Machines))
	}
}

func TestListTextShowsManagedMachines(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{{
			Tenant:    "acme",
			Project:   "default",
			Name:      "codex",
			PrivateIP: "10.248.0.20",
			AppPort:   3000,
			Running:   true,
		}}},
	}, "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "default") || !strings.Contains(stdout, "codex") {
		t.Fatalf("stdout = %q, want machine project and name", stdout)
	}
	if !strings.Contains(stdout, "Unmanaged: 0") {
		t.Fatalf("stdout = %q, want unmanaged count", stdout)
	}
}

func TestListUsesProjectFromEnv(t *testing.T) {
	t.Setenv("SANDCASTLE_PROJECT", "website")
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{{
			Tenant: "acme", Project: "default", Name: "builder", PrivateIP: "10.248.0.20", AppPort: 3000,
		}, {
			Tenant: "acme", Project: "website", Name: "codex", PrivateIP: "10.248.0.21", AppPort: 3000,
		}}},
	}, "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "website") || !strings.Contains(stdout, "codex") {
		t.Fatalf("stdout = %q, want website/codex", stdout)
	}
	if strings.Contains(stdout, "builder") {
		t.Fatalf("stdout = %q, want env project filter to hide default/builder", stdout)
	}
}

func TestListShowsUnmanagedCountWithoutFlag(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{unmanaged: []machine.UnmanagedMachine{{
			Tenant: "acme", Name: "manual", InstanceName: "manual", Status: "Running", Running: true,
		}}},
	}, "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Unmanaged: 1") {
		t.Fatalf("stdout = %q, want unmanaged count", stdout)
	}
	if strings.Contains(stdout, "manual") {
		t.Fatalf("stdout = %q, unmanaged row should be hidden without -u", stdout)
	}
}

func TestListIncludesUnmanagedWithFlagTenantWide(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{unmanaged: []machine.UnmanagedMachine{{
			Tenant: "acme", Name: "manual", InstanceName: "manual", Status: "Running", Running: true,
		}}},
	}, "list", "-u")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "manual") || !strings.Contains(stdout, "unmanaged:Running") || !strings.Contains(stdout, "Unmanaged: 1") {
		t.Fatalf("stdout = %q, want unmanaged row and count", stdout)
	}
}

func TestListProjectScopeHidesUnmanagedRowsButShowsCount(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{unmanaged: []machine.UnmanagedMachine{{
			Tenant: "acme", Name: "manual", InstanceName: "manual", Status: "Running", Running: true,
		}}},
	}, "list", "default", "-u")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Unmanaged: 1") {
		t.Fatalf("stdout = %q, want unmanaged count", stdout)
	}
	if strings.Contains(stdout, "manual") {
		t.Fatalf("stdout = %q, unmanaged row should be hidden for project-scoped list", stdout)
	}
}

func TestProjectListShowsCurrentTenantProjects(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "project", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "default") || !strings.Contains(stdout, "website") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestProjectStatusShowsMachineCount(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{
			{Tenant: "acme", Project: "website", Name: "codex"},
			{Tenant: "acme", Project: "default", Name: "shell"},
		}},
	}, "project", "status", "website")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Project: website") || !strings.Contains(stdout, "Machines: 1") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestProjectStatusJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{
			{Tenant: "acme", Project: "website", Name: "codex"},
		}},
	}, "--output", "json", "project", "status", "website")
	if err != nil {
		t.Fatal(err)
	}
	var payload projectStatusPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Tenant.Tenant != "acme" || payload.Project.Name != "website" || payload.MachineCount != 1 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestProjectCreateDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "project", "create", "website", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload tenant.ProjectMutationPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Action != "create" || payload.Project.Name != "website" || len(payload.Projects) != 2 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestProjectCreateCallsUpdater(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	updater := &fakeProjectUpdater{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		tenantUpdater: updater,
	}, "project", "create", "website")
	if err != nil {
		t.Fatal(err)
	}
	if !updater.called || updater.incusProject != "sc-acme" || len(updater.projects) != 2 {
		t.Fatalf("updater = %#v", updater)
	}
}

func TestProjectDeleteRejectsNonEmptyProject(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{{Tenant: "acme", Project: "website", Name: "codex"}}},
	}, "project", "delete", "website", "--yes")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "still contains machine") {
		t.Fatalf("error = %q", err)
	}
}

func TestProjectDeleteCallsUpdater(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	updater := &fakeProjectUpdater{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore:  fakeMachineStatusStore{},
		tenantUpdater: updater,
	}, "project", "delete", "website", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !updater.called || len(updater.projects) != 1 || updater.projects[0].Name != "default" {
		t.Fatalf("updater = %#v", updater)
	}
}

func TestStatusJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "status", "acme")
	if err != nil {
		t.Fatal(err)
	}
	var payload tenant.Status
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Summary.IncusName != "sc-acme" {
		t.Fatalf("IncusName = %q", payload.Summary.IncusName)
	}
}

func TestStatusJSONUsesTenantRef(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "status", "acme")
	if err != nil {
		t.Fatal(err)
	}
	var payload tenant.Status
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Summary.Tenant != "acme" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestMachineStatusJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{{
			Tenant:         "acme",
			Project:        "default",
			Name:           "codex",
			AppPort:        5173,
			PrivateIP:      "10.248.0.20",
			LinuxUser:      "alice",
			HomeDir:        ".",
			WorkspaceDir:   "workspace",
			ContainerTools: true,
			Running:        true,
		}}},
	}, "--output", "json", "status", "codex")
	if err != nil {
		t.Fatal(err)
	}
	var payload machine.StatusResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.InstanceName != "default-codex" {
		t.Fatalf("InstanceName = %q", payload.InstanceName)
	}
	if payload.Machine.AppPort != 5173 || payload.Machine.LinuxUser != "alice" || !payload.Machine.Running {
		t.Fatalf("Machine = %#v", payload.Machine)
	}
	if !payload.Machine.ContainerTools {
		t.Fatal("ContainerTools = false, want true")
	}
}

func TestMachineStatusText(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{{
			Tenant:         "acme",
			Project:        "default",
			Name:           "codex",
			AppPort:        5173,
			PrivateIP:      "10.248.0.20",
			LinuxUser:      "alice",
			HomeDir:        ".",
			WorkspaceDir:   "workspace",
			ContainerTools: true,
			ExtraSANs:      []string{"app.example.com"},
		}}},
	}, "status", "codex")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Machine: acme/default/codex", "Instance: default-codex", "Private IP: 10.248.0.20", "Linux user: alice", "Container tools: enabled", "Extra SANs: app.example.com"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestStatusRejectsAmbiguousBareMachine(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{
			{Tenant: "acme", Project: "default", Name: "codex"},
			{Tenant: "acme", Project: "website", Name: "codex"},
		}},
	}, "status", "codex")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %q", err)
	}
}

func TestCreateDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "create", "codex", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload machine.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.PrivateIP != "10.248.0.20" {
		t.Fatalf("PrivateIP = %q", payload.PrivateIP)
	}
	if payload.Template != "ai" {
		t.Fatalf("Template = %q", payload.Template)
	}
	if payload.HomeDir != "default/codex" || payload.WorkspaceDir != "default/codex" {
		t.Fatalf("HomeDir/WorkspaceDir = %q/%q, want default/codex", payload.HomeDir, payload.WorkspaceDir)
	}
	if payload.LinuxUser != "acme" {
		t.Fatalf("LinuxUser = %q", payload.LinuxUser)
	}
}

func TestCreateDryRunUsesProjectFromEnv(t *testing.T) {
	t.Setenv("SANDCASTLE_PROJECT", "website")
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "create", "codex", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload machine.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Tenant.Tenant != "acme" || payload.Project != "website" || payload.Name != "codex" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.LinuxUser != "acme" {
		t.Fatalf("LinuxUser = %q", payload.LinuxUser)
	}
}

func TestCreateDryRunSupportsTemplateAndStorageFlags(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "create", "minimal", "--dry-run", "--template", "base", "--home-dir", "shared-home", "--workspace-dir", ".")
	if err != nil {
		t.Fatal(err)
	}
	var payload machine.CreatePlan
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

func TestCreateDryRunSupportsContainerTools(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "create", "codex", "--dry-run", "--container-tools")
	if err != nil {
		t.Fatal(err)
	}
	var payload machine.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.ContainerTools {
		t.Fatal("ContainerTools = false, want true")
	}
	state, err := meta.ParseMachineConfig(payload.MetadataConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !state.ContainerTools {
		t.Fatal("metadata ContainerTools = false, want true")
	}
}

func TestCreateDryRunRejectsUnsafeStorageFlags(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "create", "minimal", "--dry-run", "--home-dir", "../shared")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "must not contain .. path segments") {
		t.Fatalf("error = %q", err)
	}
}

func TestCreateDetachSkipsConnect(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	creator := &fakeMachineCreator{}
	connector := &fakeMachineConnector{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineCreator:   creator,
		machineConnector: connector,
	}, "create", "codex", "--detach")
	if err != nil {
		t.Fatal(err)
	}
	if creator.plan.InstanceName != "default-codex" {
		t.Fatalf("created instance = %q", creator.plan.InstanceName)
	}
	if connector.called {
		t.Fatal("expected create --detach to skip connect")
	}
}

func TestCreateBackgroundSkipsConnect(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	creator := &fakeMachineCreator{}
	connector := &fakeMachineConnector{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineCreator:   creator,
		machineConnector: connector,
	}, "create", "codex", "--background")
	if err != nil {
		t.Fatal(err)
	}
	if creator.plan.InstanceName != "default-codex" {
		t.Fatalf("created instance = %q", creator.plan.InstanceName)
	}
	if connector.called {
		t.Fatal("expected create --background to skip connect")
	}
}

func TestCreateConnectsAfterCreateByDefault(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	creator := &fakeMachineCreator{}
	connector := &fakeMachineConnector{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineCreator:   creator,
		machineConnector: connector,
	}, "create", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if creator.plan.InstanceName != "default-codex" {
		t.Fatalf("created instance = %q", creator.plan.InstanceName)
	}
	if !connector.called {
		t.Fatal("expected create to connect to machine")
	}
	if connector.plan.InstanceName != "default-codex" {
		t.Fatalf("connected instance = %q", connector.plan.InstanceName)
	}
}

func TestConnectCommandUsesConnector(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := &fakeMachineConnector{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineConnector: connector,
	}, "connect", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if !connector.called {
		t.Fatal("expected machine connector call")
	}
	if connector.plan.InstanceName != "default-codex" {
		t.Fatalf("connected instance = %q", connector.plan.InstanceName)
	}
	if !connector.plan.Interactive {
		t.Fatal("expected default connect to be interactive")
	}
}

func TestConnectCommandAcceptsExplicitCommand(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := &fakeMachineConnector{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineConnector: connector,
	}, "connect", "codex", "pwd")
	if err != nil {
		t.Fatal(err)
	}
	if connector.plan.Interactive {
		t.Fatal("expected explicit connect command to be non-interactive")
	}
	if len(connector.plan.Command) != 1 || connector.plan.Command[0] != "pwd" {
		t.Fatalf("Command = %#v", connector.plan.Command)
	}
}

func TestConnectCommandSearchesBareMachineWhenUnique(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := &fakeMachineConnector{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore:     fakeMachineStatusStore{machines: []meta.Machine{{Tenant: "acme", Project: "website", Name: "codex"}}},
		machineConnector: connector,
	}, "connect", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if connector.plan.Project != "website" || connector.plan.InstanceName != "website-codex" {
		t.Fatalf("connector.plan = %#v", connector.plan)
	}
}

func TestConnectCommandRejectsAmbiguousBareMachine(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{
			{Tenant: "acme", Project: "default", Name: "codex"},
			{Tenant: "acme", Project: "website", Name: "codex"},
		}},
		machineConnector: &fakeMachineConnector{},
	}, "connect", "codex")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %q", err)
	}
}

func TestMachineDeleteRequiresConfirmation(t *testing.T) {
	_, err := executeForTest(t, "sandcastle", "delete", "codex")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %q, want --yes hint", err.Error())
	}
}

func TestMachineDeleteCallsExecutor(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	controller := &fakeMachineController{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		adminConfig: scconfig.Admin{
			Tenant:                "acme",
			Remote:                scconfig.DefaultRemote,
			StoragePool:           scconfig.DefaultStoragePool,
			CIDRPool:              scconfig.DefaultCIDRPool,
			IncusProjectPrefix:    scconfig.DefaultIncusProjectPrefix,
			InfrastructureProject: scconfig.DefaultInfrastructureProject,
			Images:                scconfig.Images{Base: scconfig.DefaultBaseImageAlias, AI: scconfig.DefaultAIImageAlias},
		},
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{
			machines: []meta.Machine{{Tenant: "acme", Project: "default", Name: "codex"}},
		},
		machineControl: controller,
	}, "delete", "codex", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !controller.called || controller.plan.Action != machine.ActionDelete {
		t.Fatalf("controller.plan = %#v", controller.plan)
	}
}

func TestPortSetRejectsInvalidPort(t *testing.T) {
	_, err := executeForTest(t, "sandcastle", "port", "set", "codex", "bad")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDNSStatusJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "dns", "status", "acme")
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
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "dns", "install", "acme", "--dry-run")
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

func TestFormatLocalDNSPlanShowsResolverCommands(t *testing.T) {
	output := formatLocalDNSPlan("Install", localdns.Plan{
		Reference:        "acme",
		DNSEndpoint:      "10.248.0.53:53",
		Listen:           "127.0.0.1:53541",
		ResolverStrategy: localdns.StrategySystemdResolve,
		ResolverCommands: []localdns.Command{
			{Args: []string{"resolvectl", "dns", "lo", "127.0.0.1:53541"}},
			{Args: []string{"resolvectl", "domain", "lo", "~acme"}},
		},
	})
	for _, want := range []string{
		"Resolver: systemd-resolved",
		"Resolver commands:",
		"resolvectl dns lo 127.0.0.1:53541",
		"resolvectl domain lo ~acme",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestDNSRefreshRunsLocalDNSExecutor(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeLocalDNSManager{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		localDNS: manager,
	}, "dns", "refresh", "acme")
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
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "tailscale", "up", "acme", "--auth-key", "tskey-secret", "--dry-run")
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
	if payload.InstanceName != "sc-acme" {
		t.Fatalf("InstanceName = %q", payload.InstanceName)
	}
	if !payload.HasAuthKey {
		t.Fatal("expected HasAuthKey")
	}
}

func TestTailscaleUpDryRunUsesDefaultAdvertiseTag(t *testing.T) {
	t.Setenv("SANDCASTLE_E2E_TAILSCALE_TAG", "")
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "tailscale", "up", "acme", "--dry-run")
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

func TestTailscaleUpUsesCurrentTenant(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "tailscale", "up", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload tailscale.UpPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Reference != "acme" || payload.InstanceName != "sc-acme" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestTailscaleUpDryRunRejectsInvalidAdvertiseTag(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "tailscale", "up", "acme", "--advertise-tag", "sandcastle", "--dry-run")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Tailscale advertise tag") {
		t.Fatalf("error = %q", err)
	}
}

func TestTailscaleUpRunsExecutor(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeTailscaleRunner{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		tailscale: runner,
	}, "tailscale", "up", "acme", "--auth-key", "tskey-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !runner.called {
		t.Fatal("expected tailscale runner call")
	}
	if runner.plan.InstanceName != "sc-acme" {
		t.Fatalf("InstanceName = %q", runner.plan.InstanceName)
	}
	if runner.plan.AuthKey != "tskey-secret" {
		t.Fatalf("AuthKey = %q", runner.plan.AuthKey)
	}
}

func TestTailscaleStatusRunsExecutor(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeTailscaleRunner{status: tailscale.StatusResult{
		Reference: "acme",
		Tailscale: meta.Tailscale{State: "Running", TailscaleIPs: []string{"100.80.12.34"}},
	}}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		tailscale: runner,
	}, "--output", "json", "tailscale", "status", "acme")
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

func TestTailscaleStatusUsesCurrentTenant(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeTailscaleRunner{status: tailscale.StatusResult{
		Reference: "acme",
		Tailscale: meta.Tailscale{State: "Running"},
	}}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		tailscale: runner,
	}, "tailscale", "status")
	if err != nil {
		t.Fatal(err)
	}
	if !runner.statusCalled || runner.statusPlan.Reference != "acme" {
		t.Fatalf("runner = %#v", runner)
	}
}

func TestTailscaleDownDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "tailscale", "down", "acme", "--dry-run")
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

func TestTailscaleDownUsesCurrentTenant(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "tailscale", "down", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload tailscale.DownPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Reference != "acme" || payload.InstanceName != "sc-acme" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestHostOverrideAddDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		hostMachine: fakeHostMachineStore{},
	}, "--output", "json", "host", "override", "create", "codex", "Example.COM", "--dry-run")
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

func TestHostOverrideAddAppliesMachineAndHosts(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeHostOverrideManager{}
	files := &fakeHostFiles{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		hostMachine:   fakeHostMachineStore{},
		hostOverrides: manager,
		hostFiles:     files,
	}, "host", "override", "create", "codex", "example.com")
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

func TestHostOverrideListUsesCurrentTenant(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		hostMachine: fakeHostMachineStore{},
	}, "--output", "json", "host", "override", "list")
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

func TestHostOverrideListAcceptsExplicitTenant(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		hostMachine: fakeHostMachineStore{},
	}, "--output", "json", "host", "override", "list", "acme")
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

func TestHostOverrideDeleteAppliesMachineAndHosts(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeHostOverrideManager{}
	files := &fakeHostFiles{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		hostMachine:   fakeHostMachineStore{},
		hostOverrides: manager,
		hostFiles:     files,
	}, "host", "override", "delete", "codex", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.deleted {
		t.Fatal("expected host override delete call")
	}
	if !files.deleted {
		t.Fatal("expected hosts file delete call")
	}
}

func TestTrustInstallDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "trust", "install", "acme", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload localtrust.Plan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.CAVolume != tenant.CAVolumeName {
		t.Fatalf("CAVolume = %q", payload.CAVolume)
	}
	if !strings.Contains(payload.Warning, "mint certificates") {
		t.Fatalf("Warning = %q", payload.Warning)
	}
}

func TestTrustInstallRunsExecutor(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeLocalTrustManager{}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		localTrust: manager,
	}, "trust", "install", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.installed {
		t.Fatal("expected local trust install call")
	}
	if !strings.Contains(stdout, "Warning: Trusting this tenant CA") {
		t.Fatalf("stdout missing pre-install trust warning: %q", stdout)
	}
	if !strings.Contains(stdout, "install tenant CA trust: acme") {
		t.Fatalf("stdout missing trust result: %q", stdout)
	}
	if manager.plan.IncusProject != "sc-acme" {
		t.Fatalf("IncusProject = %q", manager.plan.IncusProject)
	}
}

func TestTrustUninstallRunsExecutor(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeLocalTrustManager{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		localTrust: manager,
	}, "trust", "uninstall", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.deleted {
		t.Fatal("expected local trust uninstall call")
	}
}

func TestRouteCreateDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: routeAdminConfigForTest(),
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		routeMachine: fakeRouteMachineStore{},
	}, "--output", "json", "route", "create", "App.Example.COM", "codex", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload route.CreatePlan
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

func TestRouteCreateDryRunTextShowsDNSProofTarget(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: routeAdminConfigForTest(),
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		routeMachine: fakeRouteMachineStore{},
	}, "route", "create", "App.Example.COM", "codex", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Route: app.example.com -> acme/default/codex:5173") {
		t.Fatalf("stdout missing route: %q", stdout)
	}
	if !strings.Contains(stdout, "DNS proof: app.example.com must resolve to 203.0.113.10") {
		t.Fatalf("stdout missing DNS proof target: %q", stdout)
	}
}

func TestRouteCreateRequiresBrokerExecutor(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: routeAdminConfigForTest(),
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		routeMachine: fakeRouteMachineStore{},
	}, "route", "create", "app.example.com", "codex")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "route broker") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestRouteStatusShowsMatchingRoute(t *testing.T) {
	routes := &fakeRouteManager{list: route.ListResult{Routes: []route.Route{
		{Hostname: "app.example.com", TargetReference: "acme/default/codex", RoutePort: 5173},
		{Hostname: "other.example.com", TargetReference: "acme/default/shell", RoutePort: 3000},
	}}}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: routeAdminConfigForTest(),
		routes:      routes,
	}, "route", "status", "App.Example.COM.")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout) != "app.example.com -> acme/default/codex:5173" {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestRouteStatusJSON(t *testing.T) {
	routes := &fakeRouteManager{list: route.ListResult{Routes: []route.Route{
		{Hostname: "app.example.com", TargetReference: "acme/default/codex", RoutePort: 5173},
	}}}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: routeAdminConfigForTest(),
		routes:      routes,
	}, "--output", "json", "route", "status", "app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	var payload route.Route
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Hostname != "app.example.com" || payload.TargetReference != "acme/default/codex" || payload.RoutePort != 5173 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestRouteStatusRequiresExistingRoute(t *testing.T) {
	_, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: routeAdminConfigForTest(),
		routes:      &fakeRouteManager{},
	}, "route", "status", "missing.example.com")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "public route missing.example.com not found") {
		t.Fatalf("error = %q", err)
	}
}

func routeAdminConfigForTest() scconfig.Admin {
	admin := scconfig.LoadAdminFromEnv()
	admin.Tenant = "acme"
	admin.InfrastructureHost = "203.0.113.10"
	return admin
}

func TestRouteManagerFromEnvUsesBrokerClient(t *testing.T) {
	t.Setenv("SANDCASTLE_ROUTE_BROKER_URL", " https://broker.example.com/ ")
	t.Setenv("SANDCASTLE_ROUTE_BROKER_CLIENT_CERT", " /tmp/client.crt ")
	t.Setenv("SANDCASTLE_ROUTE_BROKER_CLIENT_KEY", " /tmp/client.key ")
	t.Setenv("SANDCASTLE_ROUTE_BROKER_INSECURE_SKIP_VERIFY", " 1 ")

	manager := routeManagerFromEnv()
	client, ok := manager.(routebroker.Client)
	if !ok {
		t.Fatalf("manager = %T, want routebroker.Client", manager)
	}
	if client.BaseURL != "https://broker.example.com/" || client.CertFile != "/tmp/client.crt" || client.KeyFile != "/tmp/client.key" {
		t.Fatalf("client = %#v", client)
	}
	if !client.InsecureSkipVerify {
		t.Fatal("expected insecure skip verify flag")
	}
}

func TestRouteManagerFromEnvRequiresBrokerURL(t *testing.T) {
	if manager := routeManagerFromEnv(); manager != nil {
		t.Fatalf("manager = %T, want nil without broker URL", manager)
	}
}

func TestAdminVersion(t *testing.T) {
	stdout, err := executeAdminForTest(t, "sandcastle-admin", "version")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(stdout); got != version {
		t.Fatalf("admin version output = %q, want %q", got, version)
	}
}

func TestAdminVersionHelpUsesAdminWording(t *testing.T) {
	stdout, err := executeAdminForTest(t, "sandcastle-admin", "version", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Print the Sandcastle admin command version") {
		t.Fatalf("admin version help = %q", stdout)
	}
}

func TestAdminTenantListJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name: "sandcastle-admin",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "tenant", "list")
	if err != nil {
		t.Fatal(err)
	}
	var payload tenantListPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Tenants) != 1 {
		t.Fatalf("len(payload.Tenants) = %d, want 1", len(payload.Tenants))
	}
	if payload.Tenants[0].IncusName != "sc-acme" {
		t.Fatalf("IncusName = %q", payload.Tenants[0].IncusName)
	}
}

func TestAdminTenantCreateDryRunJSON(t *testing.T) {
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "tenant", "create", "acme", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload tenant.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.IncusProject != "sc-acme" {
		t.Fatalf("IncusProject = %q", payload.IncusProject)
	}
	if payload.PrivateCIDR != "10.248.0.0/24" {
		t.Fatalf("PrivateCIDR = %q", payload.PrivateCIDR)
	}
}

func TestAdminTenantCreateRequiresExecutor(t *testing.T) {
	_, err := executeAdminForTest(t, "sandcastle-admin", "tenant", "create", "acme")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "executor") {
		t.Fatalf("error = %q, want executor hint", err.Error())
	}
}

func TestAdminTenantCreateRejectsKnownTLD(t *testing.T) {
	creator := &fakeTenantCreator{}
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:          "sandcastle-admin",
		tenantCreator: creator,
	}, "tenant", "create", "test")
	if err == nil {
		t.Fatal("expected known TLD error")
	}
	if !strings.Contains(err.Error(), "denied special-use suffix") {
		t.Fatalf("error = %q", err.Error())
	}
	if creator.called {
		t.Fatal("creator should not be called for invalid tenant")
	}
}

func TestAdminMachineListJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name: "sandcastle-admin",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{{
			Tenant: "acme", Project: "default", Name: "codex", PrivateIP: "10.248.0.20", AppPort: 3000,
		}, {
			Tenant: "acme", Project: "website", Name: "codex", PrivateIP: "10.248.0.21", AppPort: 3000,
		}}},
	}, "--output", "json", "list", "acme")
	if err != nil {
		t.Fatal(err)
	}
	var payload listPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Tenant.Tenant != "acme" || !payload.AllProjects || len(payload.Machines) != 2 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestAdminMachineListProjectFilters(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name: "sandcastle-admin",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{{
			Tenant: "acme", Project: "default", Name: "builder", PrivateIP: "10.248.0.20", AppPort: 3000,
		}, {
			Tenant: "acme", Project: "website", Name: "codex", PrivateIP: "10.248.0.21", AppPort: 3000,
		}}},
	}, "list", "acme/website")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "website") || !strings.Contains(stdout, "codex") {
		t.Fatalf("stdout = %q, want website/codex", stdout)
	}
	if strings.Contains(stdout, "builder") {
		t.Fatalf("stdout = %q, want project filter to hide default/builder", stdout)
	}
}

func TestAdminMachineCreateDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name: "sandcastle-admin",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "create", "acme/codex", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload machine.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Tenant.Tenant != "acme" || payload.Project != "default" || payload.InstanceName != "default-codex" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Reference != "acme/default/codex" {
		t.Fatalf("Reference = %q", payload.Reference)
	}
}

func TestAdminMachineCreateExplicitProjectDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name: "sandcastle-admin",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "create", "acme/website/codex", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload machine.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Project != "website" || payload.InstanceName != "website-codex" || payload.Reference != "acme/website/codex" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestAdminMachineConnectUsesTenantRef(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := &fakeMachineConnector{}
	_, err = executeAdminForTestWithConfig(t, commandConfig{
		name: "sandcastle-admin",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineConnector: connector,
	}, "connect", "acme/codex", "pwd")
	if err != nil {
		t.Fatal(err)
	}
	if !connector.called || connector.plan.Reference != "acme/default/codex" || connector.plan.InstanceName != "default-codex" {
		t.Fatalf("connector.plan = %#v", connector.plan)
	}
}

func TestAdminMachineDeleteRequiresConfirmation(t *testing.T) {
	_, err := executeAdminForTest(t, "sandcastle-admin", "delete", "acme/codex")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %q", err)
	}
}

func TestAdminMachineDeleteCallsExecutor(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	controller := &fakeMachineController{}
	_, err = executeAdminForTestWithConfig(t, commandConfig{
		name: "sandcastle-admin",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineControl: controller,
	}, "delete", "acme/codex", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !controller.called || controller.plan.Reference != "acme/default/codex" || controller.plan.InstanceName != "default-codex" || controller.plan.Action != machine.ActionDelete {
		t.Fatalf("controller.plan = %#v", controller.plan)
	}
}

func TestAdminTLDRefreshWritesSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tlds":
			_, _ = w.Write([]byte("# Version 2026050700\nCOM\nORG\n"))
		case "/special-use":
			_, _ = w.Write([]byte("Name,Reference\nLOCAL.,[RFC6762]\nTEST.,[RFC6761]\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	output := filepath.Join(dir, "tld_snapshot_generated.go")
	specialUseOutput := filepath.Join(dir, "special_use_snapshot_generated.go")
	stdout, err := executeAdminForTest(t, "sandcastle-admin", "tld", "refresh", "--source-url", server.URL+"/tlds", "--output-file", output, "--special-use-source-url", server.URL+"/special-use", "--special-use-output-file", specialUseOutput)
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
	specialUseContent, err := os.ReadFile(specialUseOutput)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(specialUseContent), `"local": true`) {
		t.Fatalf("special use content = %s", string(specialUseContent))
	}
}

func TestAdminTLDRefreshDryRunJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tlds":
			_, _ = w.Write([]byte("COM\nORG\n"))
		case "/special-use":
			_, _ = w.Write([]byte("Name,Reference\nLOCAL.,[RFC6762]\nTEST.,[RFC6761]\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	output := filepath.Join(dir, "tld_snapshot_generated.go")
	specialUseOutput := filepath.Join(dir, "special_use_snapshot_generated.go")
	stdout, err := executeAdminForTest(t, "sandcastle-admin", "--output", "json", "tld", "refresh", "--source-url", server.URL+"/tlds", "--output-file", output, "--special-use-source-url", server.URL+"/special-use", "--special-use-output-file", specialUseOutput, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload domain.DenyListRefreshResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.TLD.Count != 2 || payload.TLD.Written || payload.SpecialUse.Count != 2 || payload.SpecialUse.Written {
		t.Fatalf("payload = %#v", payload)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("expected dry run not to write output, stat err = %v", err)
	}
	if _, err := os.Stat(specialUseOutput); !os.IsNotExist(err) {
		t.Fatalf("expected dry run not to write special-use output, stat err = %v", err)
	}
}

func TestAdminTenantDeleteRequiresConfirmation(t *testing.T) {
	_, err := executeAdminForTest(t, "sandcastle-admin", "tenant", "delete", "acme")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %q, want --yes hint", err.Error())
	}
}

func TestAdminInfraCreateDryRunJSON(t *testing.T) {
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "infra", "create", "--dry-run")
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
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		infraCreator: creator,
	}, "infra", "create")
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
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "infra", "create")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "infrastructure creation executor") {
		t.Fatalf("error = %q", err)
	}
}

func TestAdminInfraDeleteRequiresConfirmation(t *testing.T) {
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "infra", "delete")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %q", err)
	}
}

func TestAdminInfraDeleteCallsExecutor(t *testing.T) {
	deleter := &fakeInfraDeleter{}
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		infraDeleter: deleter,
	}, "infra", "delete", "--yes")
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
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "image", "sync", "sandcastle/base:debian-13", "--dry-run")
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
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "image", "build", "base", "--tag", "sandcastle/base:debian-13", "--dry-run")
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
	_, err := executeAdminForTest(t, "sandcastle-admin", "image", "build", "ai", "--dry-run")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "codex-version") {
		t.Fatalf("error = %q", err)
	}
}

func TestAdminImageBuildCallsExecutor(t *testing.T) {
	builder := &fakeImageBuilder{}
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		imageBuilder: builder,
	}, "image", "build", "base", "--tag", "sandcastle/base:debian-13")
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
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "image", "import", "base", "oci:sandcastle/base:debian-13", "--dry-run")
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
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:          "sandcastle-admin",
		adminConfig:   scconfig.LoadAdminFromEnv(),
		imageImporter: importer,
	}, "image", "import", "ai", "oci:sandcastle/ai:debian-13")
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
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		imageManager: manager,
	}, "image", "sync", "sandcastle/ai:debian-13")
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

func TestAdminTenantGrantDryRunJSON(t *testing.T) {
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "tenant", "grant", "acme", "alice", "--dry-run")
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
	if len(payload.Projects) != 1 || payload.Projects[0] != "sc-acme" {
		t.Fatalf("Projects = %#v", payload.Projects)
	}
}

func TestAdminTenantGrantCallsTrustManager(t *testing.T) {
	manager := &fakeTrustManager{}
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		trustManager: manager,
	}, "tenant", "grant", "acme", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.grantCalled || manager.plan.User != "alice" || len(manager.plan.Projects) != 1 || manager.plan.Projects[0] != "sc-acme" {
		t.Fatalf("manager = %#v", manager)
	}
}

func TestAdminTenantRevokeCallsTrustManager(t *testing.T) {
	manager := &fakeTrustManager{}
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		trustManager: manager,
	}, "tenant", "revoke", "acme", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.revokeCalled || manager.plan.User != "alice" || len(manager.plan.Projects) != 1 || manager.plan.Projects[0] != "sc-acme" {
		t.Fatalf("manager = %#v", manager)
	}
}

func TestAdminTenantUsersListsTrustUsers(t *testing.T) {
	manager := &fakeTrustManager{tenantUsers: usertrust.TenantUsersResult{
		Tenant:       "acme",
		IncusProject: "sc-acme",
		Users:        []string{"alice", "bob"},
	}}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		trustManager: manager,
	}, "tenant", "users", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.usersCalled {
		t.Fatal("expected tenant users manager call")
	}
	if !strings.Contains(stdout, "Users: alice, bob") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestAdminUserCreateDryRunShowsRemoteName(t *testing.T) {
	stdout, err := executeAdminForTest(t, "sandcastle-admin", "user", "create", "alice", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Remote: sandcastle-alice") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestAdminTenantGrantRejectsInvalidTenantRef(t *testing.T) {
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "tenant", "grant", "bob/default", "alice", "--dry-run")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid tenant") {
		t.Fatalf("error = %q", err)
	}
}

func TestAdminUserTokenShowsBootstrapCommands(t *testing.T) {
	manager := &fakeTrustManager{token: "certificate-add-token"}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		trustManager: manager,
	}, "user", "token", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.tokenCalled {
		t.Fatal("expected token manager to be called")
	}
	for _, want := range []string{
		"Remote: sandcastle-alice",
		"sc remote add sandcastle-alice certificate-add-token",
		"sc config set tenant <tenant>",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestAdminUserTokenJSONIncludesRemoteName(t *testing.T) {
	manager := &fakeTrustManager{token: "certificate-add-token"}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		trustManager: manager,
	}, "--output", "json", "user", "token", "alice")
	if err != nil {
		t.Fatal(err)
	}
	var payload usertrust.TokenResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.RemoteName != "sandcastle-alice" {
		t.Fatalf("RemoteName = %q", payload.RemoteName)
	}
}

func TestAdminRouteBrokerServeCallsRunner(t *testing.T) {
	runner := &fakeRouteBrokerRunner{}
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		routeBroker: runner,
	}, "route-broker", "serve", "--listen", "127.0.0.1:9443", "--cert", "/tmp/broker.crt", "--key", "/tmp/broker.key")
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
	_, err := executeAdminForTest(t, "sandcastle-admin", "route-broker", "serve", "--cert", "/tmp/broker.crt", "--key", "/tmp/broker.key")
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

type fakeMachineCreator struct {
	plan machine.CreatePlan
}

func (f *fakeMachineCreator) CreateMachine(ctx context.Context, plan machine.CreatePlan) error {
	f.plan = plan
	return nil
}

type fakeMachineConnector struct {
	called bool
	plan   machine.ConnectPlan
}

func (f *fakeMachineConnector) ConnectMachine(ctx context.Context, plan machine.ConnectPlan, session machine.ConnectSession) error {
	f.called = true
	f.plan = plan
	return nil
}

type fakeMachineController struct {
	called bool
	plan   machine.LifecyclePlan
}

func (f *fakeMachineController) ApplyLifecycle(ctx context.Context, plan machine.LifecyclePlan) error {
	f.called = true
	f.plan = plan
	return nil
}

type fakeTenantCreator struct {
	called bool
	plan   tenant.CreatePlan
}

func (f *fakeTenantCreator) CreateTenant(ctx context.Context, plan tenant.CreatePlan) error {
	f.called = true
	f.plan = plan
	return nil
}

type fakeProjectUpdater struct {
	called       bool
	incusProject string
	projects     []meta.Project
}

func (f *fakeProjectUpdater) SetTenantProjects(ctx context.Context, incusProjectName string, projects []meta.Project) error {
	f.called = true
	f.incusProject = incusProjectName
	f.projects = append([]meta.Project{}, projects...)
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
	statusPlan   tailscale.StatusPlan
	downPlan     tailscale.DownPlan
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
	f.statusPlan = plan
	return f.status, nil
}

func (f *fakeTailscaleRunner) RunDown(ctx context.Context, plan tailscale.DownPlan, session tailscale.RunSession) error {
	f.downCalled = true
	f.downPlan = plan
	return nil
}

type fakeHostMachineStore struct{}

func (f fakeHostMachineStore) FindMachine(ctx context.Context, summary tenant.Summary, projectName string, name string) (meta.Machine, error) {
	return meta.Machine{
		Tenant:    summary.Tenant,
		Project:   projectName,
		Name:      name,
		AppPort:   3000,
		PrivateIP: "10.248.0.20",
		ExtraSANs: []string{"example.com"},
	}, nil
}

func (f fakeHostMachineStore) ListMachines(ctx context.Context, summary tenant.Summary) ([]meta.Machine, error) {
	machine, err := f.FindMachine(ctx, summary, "default", "codex")
	if err != nil {
		return nil, err
	}
	return []meta.Machine{machine}, nil
}

type fakeMachineStatusStore struct {
	machines  []meta.Machine
	unmanaged []machine.UnmanagedMachine
}

func (f fakeMachineStatusStore) ListMachines(ctx context.Context, summary tenant.Summary) ([]meta.Machine, error) {
	return f.machines, nil
}

func (f fakeMachineStatusStore) ListUnmanagedMachines(ctx context.Context, summary tenant.Summary) ([]machine.UnmanagedMachine, error) {
	return f.unmanaged, nil
}

type fakeHostOverrideManager struct {
	called  bool
	deleted bool
	plan    hostoverride.AddPlan
}

func (f *fakeHostOverrideManager) Add(ctx context.Context, plan hostoverride.AddPlan) error {
	f.called = true
	f.plan = plan
	return nil
}

func (f *fakeHostOverrideManager) Delete(ctx context.Context, plan hostoverride.DeletePlan) error {
	f.deleted = true
	return nil
}

type fakeRouteMachineStore struct{}

func (f fakeRouteMachineStore) FindMachine(ctx context.Context, summary tenant.Summary, projectName string, name string) (meta.Machine, error) {
	return meta.Machine{
		Tenant:    summary.Tenant,
		Project:   projectName,
		Name:      name,
		AppPort:   5173,
		PrivateIP: "10.248.0.20",
	}, nil
}

type fakeRouteManager struct {
	list route.ListResult
}

func (f *fakeRouteManager) Create(ctx context.Context, plan route.CreatePlan) error {
	return nil
}

func (f *fakeRouteManager) Delete(ctx context.Context, plan route.DeletePlan) error {
	return nil
}

func (f *fakeRouteManager) List(ctx context.Context, plan route.ListPlan) (route.ListResult, error) {
	return f.list, nil
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

type fakeTrustManager struct {
	tokenCalled  bool
	grantCalled  bool
	revokeCalled bool
	usersCalled  bool
	plan         usertrust.UserPlan
	usersPlan    usertrust.TenantUsersPlan
	tenantUsers  usertrust.TenantUsersResult
	token        string
}

func (f *fakeTrustManager) Grant(ctx context.Context, plan usertrust.UserPlan) error {
	f.grantCalled = true
	f.plan = plan
	return nil
}

func (f *fakeTrustManager) Revoke(ctx context.Context, plan usertrust.UserPlan) error {
	f.revokeCalled = true
	f.plan = plan
	return nil
}

func (f *fakeTrustManager) ListTenantUsers(ctx context.Context, plan usertrust.TenantUsersPlan) (usertrust.TenantUsersResult, error) {
	f.usersCalled = true
	f.usersPlan = plan
	if f.tenantUsers.Tenant == "" {
		return usertrust.TenantUsersResult{Tenant: plan.Tenant, IncusProject: plan.IncusProject}, nil
	}
	return f.tenantUsers, nil
}

func (f *fakeTrustManager) CreateToken(ctx context.Context, plan usertrust.UserPlan) (usertrust.TokenResult, error) {
	f.tokenCalled = true
	f.plan = plan
	return usertrust.TokenResult{
		User:            plan.User,
		CertificateName: plan.CertificateName,
		RemoteName:      plan.RemoteName,
		Restricted:      plan.Restricted,
		Projects:        plan.Projects,
		Token:           f.token,
	}, nil
}

type fakeHostFiles struct {
	called  bool
	deleted bool
	plan    hostoverride.AddPlan
}

func (f *fakeHostFiles) AddHostsEntry(ctx context.Context, plan hostoverride.AddPlan) error {
	f.called = true
	f.plan = plan
	return nil
}

func (f *fakeHostFiles) RemoveHostsEntry(ctx context.Context, plan hostoverride.DeletePlan) error {
	f.deleted = true
	return nil
}

type fakeLocalTrustManager struct {
	installed bool
	deleted   bool
	plan      localtrust.Plan
}

func (f *fakeLocalTrustManager) Install(ctx context.Context, plan localtrust.Plan) (localtrust.Result, error) {
	f.installed = true
	f.plan = plan
	return localtrust.Result{Reference: plan.Reference, TrustName: plan.TrustName, Action: "install", Platform: "fake"}, nil
}

func (f *fakeLocalTrustManager) Uninstall(ctx context.Context, plan localtrust.Plan) (localtrust.Result, error) {
	f.deleted = true
	f.plan = plan
	return localtrust.Result{Reference: plan.Reference, TrustName: plan.TrustName, Action: "uninstall", Platform: "fake"}, nil
}
