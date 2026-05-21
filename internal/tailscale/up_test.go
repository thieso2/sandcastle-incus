package tailscale

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestPlanUp(t *testing.T) {
	plan, err := PlanUp(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), UpRequest{
		Reference:     "acme",
		AuthKey:       "tskey-secret",
		AdvertiseTags: []string{" tag:sandcastle,tag:sandcastle "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.InstanceName != "sc-acme" {
		t.Fatalf("InstanceName = %q", plan.InstanceName)
	}
	if len(plan.AdvertiseRoutes) != 1 || plan.AdvertiseRoutes[0] != "10.248.0.0/24" {
		t.Fatalf("AdvertiseRoutes = %#v", plan.AdvertiseRoutes)
	}
	if !plan.HasAuthKey {
		t.Fatal("expected HasAuthKey")
	}
	if len(plan.AdvertiseTags) != 1 || plan.AdvertiseTags[0] != "tag:sandcastle" {
		t.Fatalf("AdvertiseTags = %#v", plan.AdvertiseTags)
	}
	if strings.Contains(strings.Join(plan.Command, " "), "tskey-secret") {
		t.Fatalf("Command leaked auth key: %#v", plan.Command)
	}
	if !strings.Contains(strings.Join(ExecCommand(plan), " "), "tskey-secret") {
		t.Fatalf("ExecCommand missing auth key")
	}
	if !strings.Contains(strings.Join(ExecCommand(plan), " "), "tailscaled --state=/var/lib/tailscale/tailscaled.state") {
		t.Fatalf("ExecCommand missing tailscaled bootstrap: %#v", ExecCommand(plan))
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "tskey-secret") {
		t.Fatalf("plan JSON leaked auth key: %s", encoded)
	}
}

func TestPlanUpRejectsInvalidAdvertiseTags(t *testing.T) {
	for _, tag := range []string{"sandcastle", "tag:", "tag:Sandcastle", "tag:sand_castle"} {
		t.Run(tag, func(t *testing.T) {
			_, err := PlanUp(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), UpRequest{
				Reference:     "acme",
				AdvertiseTags: []string{tag},
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "Tailscale advertise tag") {
				t.Fatalf("error = %q", err)
			}
		})
	}
}

func TestPlanUpSupportsCurrentTenant(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	plan, err := PlanUp(context.Background(), admin, projectStoreForTest(t), UpRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Reference != "acme" {
		t.Fatalf("Reference = %q", plan.Reference)
	}
}

func TestPlanStatusAndParseStatus(t *testing.T) {
	plan, err := PlanStatus(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), StatusRequest{Reference: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.InstanceName != "sc-acme" {
		t.Fatalf("InstanceName = %q", plan.InstanceName)
	}
	result, err := ParseStatus("acme", plan.Tenant, []byte(`{
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

func TestParseStatusDoesNotPersistLoginURLs(t *testing.T) {
	result, err := ParseStatus("acme", project.Summary{
		Tenant:    "acme",
		IncusName: "sc-acme",
	}, []byte(`{
		"BackendState": "NeedsLogin",
		"AuthURL": "https://login.tailscale.com/a/secret-token",
		"CurrentTailnet": {"MagicDNSSuffix": "tailnet.ts.net"},
		"Self": {
			"DNSName": "sc-myproject.tailnet.ts.net.",
			"LoginURL": "https://login.tailscale.com/b/secret-token",
			"TailscaleIPs": ["100.80.12.34"],
			"PrimaryRoutes": ["10.248.0.0/24"]
		}
	}`), time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.Tailscale.State != "running-logged-out" {
		t.Fatalf("State = %q", result.Tailscale.State)
	}
	if result.Tailscale.Tailnet != "tailnet.ts.net" {
		t.Fatalf("Tailnet = %q", result.Tailscale.Tailnet)
	}
	if result.Tailscale.Hostname != "sc-myproject.tailnet.ts.net" {
		t.Fatalf("Hostname = %q", result.Tailscale.Hostname)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"login.tailscale.com", "secret-token"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("status result leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestPlanDown(t *testing.T) {
	plan, err := PlanDown(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), DownRequest{Reference: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Command, " ") != "tailscale down" {
		t.Fatalf("Command = %#v", plan.Command)
	}
}

func projectStoreForTest(t *testing.T) project.MemoryStore {
	t.Helper()
	projectConfig, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	return project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-acme",
		Config: projectConfig,
	}}}
}
