package machine

import (
	"context"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestPlanCreateDefaultsToDefaultProject(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	plan, err := PlanCreate(context.Background(), admin, tenantStoreForTest(t), nil, CreateRequest{Reference: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Tenant.Tenant != "acme" || plan.Project != "default" || plan.Name != "codex" {
		t.Fatalf("plan = %#v", plan)
	}
	if plan.InstanceName != "default-codex" {
		t.Fatalf("InstanceName = %q", plan.InstanceName)
	}
	if plan.Hostname != "codex.default.acme" {
		t.Fatalf("Hostname = %q", plan.Hostname)
	}
	if plan.CaddyFile.Content == "" || !strings.Contains(plan.CaddyFile.Content, "codex.default.acme") {
		t.Fatalf("CaddyFile = %#v", plan.CaddyFile)
	}
	if plan.Devices["home"]["source"] != project.HomeVolumeName+"/default/codex" {
		t.Fatalf("home source = %#v", plan.Devices["home"])
	}
	metadata, err := meta.ParseMachineConfig(plan.MetadataConfig)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Tenant != "acme" || metadata.Project != "default" || metadata.Name != "codex" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestPlanCreateUsesConfiguredProject(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	admin.Project = "website"
	plan, err := PlanCreate(context.Background(), admin, tenantStoreForTest(t), nil, CreateRequest{Reference: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project != "website" || plan.InstanceName != "website-codex" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanCreatePreservesExplicitProject(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	admin.Project = "website"
	plan, err := PlanCreate(context.Background(), admin, tenantStoreForTest(t), nil, CreateRequest{Reference: "default/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project != "default" || plan.InstanceName != "default-codex" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanCreateRejectsMissingTenant(t *testing.T) {
	_, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), tenantStoreForTest(t), nil, CreateRequest{Reference: "codex"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanCreateRejectsUnknownProject(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	_, err := PlanCreate(context.Background(), admin, tenantStoreForTest(t), nil, CreateRequest{Reference: "missing/codex"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanCreateReusesExistingMachineIP(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	store := fakeMachineStore{machines: []meta.Machine{{Project: "default", Name: "codex", PrivateIP: "10.248.0.42"}}}
	plan, err := PlanCreate(context.Background(), admin, tenantStoreForTest(t), store, CreateRequest{Reference: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.PrivateIP != "10.248.0.42" {
		t.Fatalf("PrivateIP = %q", plan.PrivateIP)
	}
}

func TestPlanCreateRequiresShareHomeForRunningMachineUsingSameHome(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	store := fakeMachineStore{machines: []meta.Machine{{Project: "default", Name: "other", HomeDir: "shared", Running: true}}}
	_, err := PlanCreate(context.Background(), admin, tenantStoreForTest(t), store, CreateRequest{Reference: "codex", HomeDir: "shared"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanEnter(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	plan, err := PlanEnter(context.Background(), admin, tenantStoreForTest(t), nil, EnterRequest{Reference: "website/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project != "website" || plan.InstanceName != "website-codex" || !plan.Interactive {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanEnterSearchesBareMachineWhenUnique(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	store := fakeMachineStore{machines: []meta.Machine{{Project: "website", Name: "codex"}}}
	plan, err := PlanEnter(context.Background(), admin, tenantStoreForTest(t), store, EnterRequest{Reference: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project != "website" || plan.InstanceName != "website-codex" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanEnterConnectsUnmanagedReservedMachine(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	store := fakeMachineStore{unmanaged: []UnmanagedMachine{{Name: "sc-dns", InstanceName: "sc-dns"}}}
	plan, err := PlanEnter(context.Background(), admin, tenantStoreForTest(t), store, EnterRequest{Reference: "sc-dns"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Managed || plan.InstanceName != "sc-dns" || plan.LinuxUser != "root" || plan.UserID != 0 || plan.GroupID != 0 || plan.WorkingDir != "/root" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanEnterRejectsAmbiguousBareMachine(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	store := fakeMachineStore{machines: []meta.Machine{{Project: "default", Name: "codex"}, {Project: "website", Name: "codex"}}}
	_, err := PlanEnter(context.Background(), admin, tenantStoreForTest(t), store, EnterRequest{Reference: "codex"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %q", err)
	}
}

func TestPlanLifecycle(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	plan, err := PlanLifecycle(context.Background(), admin, tenantStoreForTest(t), nil, LifecycleRequest{Reference: "codex", Action: ActionRestart})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project != "default" || plan.InstanceName != "default-codex" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestInspect(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	result, err := Inspect(context.Background(), admin, tenantStoreForTest(t), fakeMachineStore{machines: []meta.Machine{{
		Tenant:    "acme",
		Project:   "default",
		Name:      "codex",
		PrivateIP: "10.248.0.20",
	}}}, InspectRequest{Reference: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Machine.Name != "codex" || result.InstanceName != "default-codex" {
		t.Fatalf("result = %#v", result)
	}
}

func TestPlanSetPort(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	plan, err := PlanSetPort(context.Background(), admin, tenantStoreForTest(t), PortSetRequest{Reference: "website/codex", AppPort: 5173})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project != "website" || plan.InstanceName != "website-codex" || !strings.Contains(plan.CaddyFile.Content, "codex.website.acme") {
		t.Fatalf("plan = %#v", plan)
	}
}

func tenantStoreForTest(t *testing.T) project.MemoryStore {
	t.Helper()
	config, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		PrivateCIDR: "10.248.0.0/24",
		Projects: []meta.Project{
			{Name: "default"},
			{Name: "website"},
		},
		SSHPublicKey: "ssh-ed25519 test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return project.MemoryStore{Projects: []project.IncusProject{{Name: "sc-acme", Config: config}}}
}

type fakeMachineStore struct {
	machines  []meta.Machine
	unmanaged []UnmanagedMachine
}

func (s fakeMachineStore) ListMachines(ctx context.Context, summary project.Summary) ([]meta.Machine, error) {
	return s.machines, nil
}

func (s fakeMachineStore) ListUnmanagedMachines(ctx context.Context, summary project.Summary) ([]UnmanagedMachine, error) {
	return s.unmanaged, nil
}
