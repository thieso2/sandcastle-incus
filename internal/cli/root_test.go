package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
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

func TestRejectsUnknownOutputFormat(t *testing.T) {
	_, err := executeForTest(t, "sandcastle", "--output", "yaml", "version")
	if err == nil {
		t.Fatal("expected error")
	}
}
