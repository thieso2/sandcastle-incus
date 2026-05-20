package tailscale

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

func TestPlanUp(t *testing.T) {
	plan, err := PlanUp(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), UpRequest{
		Reference:     "alice/myproject",
		AuthKey:       "tskey-secret",
		AdvertiseTags: []string{"tag:sandcastle"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.InstanceName != project.TailscaleName {
		t.Fatalf("InstanceName = %q", plan.InstanceName)
	}
	if len(plan.AdvertiseRoutes) != 1 || plan.AdvertiseRoutes[0] != "10.248.0.0/24" {
		t.Fatalf("AdvertiseRoutes = %#v", plan.AdvertiseRoutes)
	}
	if !plan.HasAuthKey {
		t.Fatal("expected HasAuthKey")
	}
	if strings.Contains(strings.Join(plan.Command, " "), "tskey-secret") {
		t.Fatalf("Command leaked auth key: %#v", plan.Command)
	}
	if !strings.Contains(strings.Join(ExecCommand(plan), " "), "tskey-secret") {
		t.Fatalf("ExecCommand missing auth key")
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "tskey-secret") {
		t.Fatalf("plan JSON leaked auth key: %s", encoded)
	}
}

func TestPlanStatusAndParseStatus(t *testing.T) {
	plan, err := PlanStatus(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), StatusRequest{Reference: "alice/myproject"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.InstanceName != project.TailscaleName {
		t.Fatalf("InstanceName = %q", plan.InstanceName)
	}
	result, err := ParseStatus("alice/myproject", plan.Project, []byte(`{
		"BackendState": "Running",
		"CurrentTailnet": {"Name": "example.com"},
		"Self": {
			"HostName": "sc-myproject",
			"TailscaleIPs": ["100.80.12.34"],
			"PrimaryRoutes": ["10.248.0.0/24"]
		}
	}`), time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.Tailscale.State != "Running" {
		t.Fatalf("State = %q", result.Tailscale.State)
	}
	if result.Tailscale.Tailnet != "example.com" {
		t.Fatalf("Tailnet = %q", result.Tailscale.Tailnet)
	}
	if len(result.Tailscale.TailscaleIPs) != 1 || result.Tailscale.TailscaleIPs[0] != "100.80.12.34" {
		t.Fatalf("TailscaleIPs = %#v", result.Tailscale.TailscaleIPs)
	}
}

func TestPlanDown(t *testing.T) {
	plan, err := PlanDown(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), DownRequest{Reference: "alice/myproject"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Command, " ") != "tailscale down" {
		t.Fatalf("Command = %#v", plan.Command)
	}
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
