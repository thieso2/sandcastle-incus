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

type sandboxStoreForTest struct{}

func (s sandboxStoreForTest) FindSandbox(ctx context.Context, summary project.Summary, name string) (meta.Sandbox, error) {
	return meta.Sandbox{
		Owner:     summary.Owner,
		Project:   summary.Name,
		Name:      name,
		AppPort:   3000,
		PrivateIP: "10.248.0.20",
	}, nil
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
