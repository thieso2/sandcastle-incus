package hostoverride

import (
	"context"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

func TestPlanAdd(t *testing.T) {
	plan, err := PlanAdd(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), sandboxStoreForTest{}, AddRequest{
		Reference: "alice/myproject/codex",
		Hostname:  "Example.COM.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Hostname != "example.com" {
		t.Fatalf("Hostname = %q", plan.Hostname)
	}
	if plan.IPAddress != "10.248.0.20" {
		t.Fatalf("IPAddress = %q", plan.IPAddress)
	}
	if len(plan.ExtraSANs) != 1 || plan.ExtraSANs[0] != "example.com" {
		t.Fatalf("ExtraSANs = %#v", plan.ExtraSANs)
	}
	if !strings.Contains(plan.HostsEntry.Line, "10.248.0.20 example.com") {
		t.Fatalf("HostsEntry = %#v", plan.HostsEntry)
	}
	if !plan.RequiresReissue || !plan.RequiresHostsEdit {
		t.Fatalf("plan requirements = %#v", plan)
	}
}

func TestPlanAddSupportsProjectNameShorthandWithOwner(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Owner = "alice"
	plan, err := PlanAdd(context.Background(), admin, projectStoreForTest(t), sandboxStoreForTest{}, AddRequest{
		Reference: "myproject/codex",
		Hostname:  "example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project.Owner != "alice" || plan.Project.Name != "myproject" || plan.Sandbox.Name != "codex" {
		t.Fatalf("plan = %#v", plan)
	}
	if plan.Reference != "alice/myproject/codex" {
		t.Fatalf("Reference = %q", plan.Reference)
	}
	if !strings.Contains(plan.HostsEntry.BeginLine, "alice/myproject/codex example.com") {
		t.Fatalf("HostsEntry = %#v", plan.HostsEntry)
	}
}

func TestPlanRemoveUsesCanonicalHostsEntry(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Owner = "alice"
	plan, err := PlanRemove(context.Background(), admin, projectStoreForTest(t), sandboxStoreForTest{}, RemoveRequest{
		Reference: "myproject/codex",
		Hostname:  "example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Reference != "alice/myproject/codex" {
		t.Fatalf("Reference = %q", plan.Reference)
	}
	if !strings.Contains(plan.HostsEntry.BeginLine, "alice/myproject/codex example.com") {
		t.Fatalf("HostsEntry = %#v", plan.HostsEntry)
	}
}

func TestPlanAddRejectsHostnameAssignedToAnotherSandbox(t *testing.T) {
	_, err := PlanAdd(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), sandboxStoreWithSandboxes{
		sandboxes: []meta.Sandbox{
			{Name: "codex", PrivateIP: "10.248.0.20"},
			{Name: "web", PrivateIP: "10.248.0.21", ExtraSANs: []string{"example.com"}},
		},
	}, AddRequest{
		Reference: "alice/myproject/codex",
		Hostname:  "example.com",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "already assigned") {
		t.Fatalf("error = %q", err)
	}
}

func TestPlanAddRejectsWildcardHostname(t *testing.T) {
	_, err := PlanAdd(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), sandboxStoreForTest{}, AddRequest{
		Reference: "alice/myproject/codex",
		Hostname:  "*.example.com",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanAddRejectsIPAddress(t *testing.T) {
	_, err := PlanAdd(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), sandboxStoreForTest{}, AddRequest{
		Reference: "alice/myproject/codex",
		Hostname:  "192.0.2.1",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanRemove(t *testing.T) {
	plan, err := PlanRemove(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), sandboxStoreForTest{}, RemoveRequest{
		Reference: "alice/myproject/codex",
		Hostname:  "example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Hostname != "example.com" {
		t.Fatalf("Hostname = %q", plan.Hostname)
	}
	if plan.HostsEntry.Line != "10.248.0.20 example.com" {
		t.Fatalf("HostsEntry = %#v", plan.HostsEntry)
	}
}

func TestPlanList(t *testing.T) {
	result, err := PlanList(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), sandboxStoreForTest{}, ListRequest{Reference: "alice/myproject"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Overrides) != 1 {
		t.Fatalf("len(Overrides) = %d", len(result.Overrides))
	}
	if result.Overrides[0].Hostname != "example.com" {
		t.Fatalf("Hostname = %q", result.Overrides[0].Hostname)
	}
}

func TestPlanListSupportsProjectShorthandWithOwner(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Owner = "alice"
	result, err := PlanList(context.Background(), admin, projectStoreForTest(t), sandboxStoreForTest{}, ListRequest{Reference: "myproject"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Project.Owner != "alice" || result.Project.Name != "myproject" {
		t.Fatalf("project = %#v", result.Project)
	}
}

type sandboxStoreForTest struct{}

func (s sandboxStoreForTest) FindSandbox(ctx context.Context, summary project.Summary, name string) (meta.Sandbox, error) {
	return meta.Sandbox{
		Owner:     summary.Owner,
		Project:   summary.Name,
		Name:      name,
		AppPort:   3000,
		PrivateIP: "10.248.0.20",
		ExtraSANs: []string{"example.com"},
	}, nil
}

func (s sandboxStoreForTest) ListSandboxes(ctx context.Context, summary project.Summary) ([]meta.Sandbox, error) {
	sandbox, err := s.FindSandbox(ctx, summary, "codex")
	if err != nil {
		return nil, err
	}
	return []meta.Sandbox{sandbox}, nil
}

type sandboxStoreWithSandboxes struct {
	sandboxes []meta.Sandbox
}

func (s sandboxStoreWithSandboxes) FindSandbox(ctx context.Context, summary project.Summary, name string) (meta.Sandbox, error) {
	for _, sandbox := range s.sandboxes {
		if sandbox.Name == name {
			sandbox.Owner = summary.Owner
			sandbox.Project = summary.Name
			return sandbox, nil
		}
	}
	return meta.Sandbox{}, nil
}

func (s sandboxStoreWithSandboxes) ListSandboxes(ctx context.Context, summary project.Summary) ([]meta.Sandbox, error) {
	return s.sandboxes, nil
}

func projectStoreForTest(t *testing.T) project.MemoryStore {
	t.Helper()
	projectConfig, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	return project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-alice-myproject",
		Config: projectConfig,
	}}}
}
