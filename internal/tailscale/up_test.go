package tailscale

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
