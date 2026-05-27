package machine

import (
	"context"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
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
	if plan.HomeDir != "default" || plan.WorkspaceDir != "default" {
		t.Fatalf("storage dirs = home %q workspace %q", plan.HomeDir, plan.WorkspaceDir)
	}
	if plan.Devices["home"]["source"] != tenant.HomeVolumeName+"/default" {
		t.Fatalf("home source = %#v", plan.Devices["home"])
	}
	if plan.Devices["workspace"]["source"] != tenant.WorkspaceVolumeName+"/default" {
		t.Fatalf("workspace source = %#v", plan.Devices["workspace"])
	}
	metadata, err := meta.ParseMachineConfig(plan.MetadataConfig)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Tenant != "acme" || metadata.Project != "default" || metadata.Name != "codex" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestPlanCreateUsesTenantUnixUser(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	store := tenantStoreForTest(t)
	tenantConfig, err := meta.ParseTenantConfig(store.Projects[0].Config)
	if err != nil {
		t.Fatal(err)
	}
	tenantConfig.UnixUser = "localuser"
	store.Projects[0].Config, err = meta.TenantConfig(tenantConfig)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanCreate(context.Background(), admin, store, nil, CreateRequest{Reference: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.LinuxUser != "localuser" {
		t.Fatalf("LinuxUser = %q", plan.LinuxUser)
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

func TestPlanCreateUsesProjectDockerAutostart(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	admin.Project = "website"
	store := tenantStoreForTest(t)
	tenantConfig, err := meta.ParseTenantConfig(store.Projects[0].Config)
	if err != nil {
		t.Fatal(err)
	}
	for i := range tenantConfig.Projects {
		if tenantConfig.Projects[i].Name == "website" {
			tenantConfig.Projects[i].DockerAutostart = true
		}
	}
	store.Projects[0].Config, err = meta.TenantConfig(tenantConfig)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanCreate(context.Background(), admin, store, nil, CreateRequest{Reference: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.DockerAutostart || !plan.ContainerTools {
		t.Fatalf("DockerAutostart=%t ContainerTools=%t, want both true", plan.DockerAutostart, plan.ContainerTools)
	}
	metadata, err := meta.ParseMachineConfig(plan.MetadataConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !metadata.DockerAutostart {
		t.Fatalf("metadata DockerAutostart = false, want true")
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

func TestPlanCreateAcceptsColonProjectRef(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	admin.Project = "website"
	plan, err := PlanCreate(context.Background(), admin, tenantStoreForTest(t), nil, CreateRequest{Reference: "default:codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project != "default" || plan.Name != "codex" || plan.InstanceName != "default-codex" {
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

func TestPlanCreateSharesProjectStorageByDefault(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	store := fakeMachineStore{machines: []meta.Machine{{Project: "default", Name: "other", HomeDir: "default", WorkspaceDir: "default", Running: true}}}
	plan, err := PlanCreate(context.Background(), admin, tenantStoreForTest(t), store, CreateRequest{Reference: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.HomeDir != "default" || plan.WorkspaceDir != "default" {
		t.Fatalf("storage dirs = home %q workspace %q", plan.HomeDir, plan.WorkspaceDir)
	}
}

func TestPlanConnect(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	store := fakeMachineStore{machines: []meta.Machine{{Project: "website", Name: "codex", PrivateIP: "10.248.0.42", TailscaleIP: "100.64.0.42"}}}
	plan, err := PlanConnect(context.Background(), admin, tenantStoreForTest(t), store, ConnectRequest{Reference: "website/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project != "website" || plan.InstanceName != "website-codex" || !plan.Interactive {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanConnectSearchesBareMachineWhenUnique(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	store := fakeMachineStore{machines: []meta.Machine{{Project: "website", Name: "codex", PrivateIP: "10.248.0.42", TailscaleIP: "100.64.0.42"}}}
	plan, err := PlanConnect(context.Background(), admin, tenantStoreForTest(t), store, ConnectRequest{Reference: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project != "website" || plan.InstanceName != "website-codex" {
		t.Fatalf("plan = %#v", plan)
	}
	if plan.SSHHost != "10.248.0.42" || plan.HostKeyAlias != "codex.website.acme" || plan.Hostname != "codex.website.acme" {
		t.Fatalf("ssh target = %q alias %q", plan.SSHHost, plan.HostKeyAlias)
	}
}

func TestPlanConnectAcceptsColonProjectRef(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	admin.Project = "website"
	store := fakeMachineStore{machines: []meta.Machine{{Project: "default", Name: "codex", PrivateIP: "10.248.0.42"}}}
	plan, err := PlanConnect(context.Background(), admin, tenantStoreForTest(t), store, ConnectRequest{Reference: "default:codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project != "default" || plan.Name != "codex" || plan.InstanceName != "default-codex" {
		t.Fatalf("plan = %#v", plan)
	}
	if plan.SSHHost != "10.248.0.42" || plan.HostKeyAlias != "codex.default.acme" {
		t.Fatalf("ssh target = %q alias %q", plan.SSHHost, plan.HostKeyAlias)
	}
}

func TestPlanConnectUsesPrivateIP(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	store := fakeMachineStore{machines: []meta.Machine{{Project: "website", Name: "codex", PrivateIP: "10.248.0.42", TailscaleIP: "100.64.0.42"}}}
	plan, err := PlanConnect(context.Background(), admin, tenantStoreForTest(t), store, ConnectRequest{Reference: "website/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.SSHHost != "10.248.0.42" || plan.HostKeyAlias != "codex.website.acme" {
		t.Fatalf("ssh target = %q alias %q", plan.SSHHost, plan.HostKeyAlias)
	}
}

func TestPlanConnectUsesTenantUnixUser(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	tenantStore := tenantStoreForTest(t)
	tenantConfig, err := meta.ParseTenantConfig(tenantStore.Projects[0].Config)
	if err != nil {
		t.Fatal(err)
	}
	tenantConfig.UnixUser = "localuser"
	tenantStore.Projects[0].Config, err = meta.TenantConfig(tenantConfig)
	if err != nil {
		t.Fatal(err)
	}
	store := fakeMachineStore{machines: []meta.Machine{{Project: "website", Name: "codex", PrivateIP: "10.248.0.42"}}}
	plan, err := PlanConnect(context.Background(), admin, tenantStore, store, ConnectRequest{Reference: "website/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.LinuxUser != "localuser" {
		t.Fatalf("LinuxUser = %q", plan.LinuxUser)
	}
}

func TestPlanConnectUsesMachineLinuxUser(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	tenantStore := tenantStoreForTest(t)
	tenantConfig, err := meta.ParseTenantConfig(tenantStore.Projects[0].Config)
	if err != nil {
		t.Fatal(err)
	}
	tenantConfig.UnixUser = "localuser"
	tenantStore.Projects[0].Config, err = meta.TenantConfig(tenantConfig)
	if err != nil {
		t.Fatal(err)
	}
	store := fakeMachineStore{machines: []meta.Machine{{Project: "website", Name: "codex", PrivateIP: "10.248.0.42", LinuxUser: "machineuser"}}}
	plan, err := PlanConnect(context.Background(), admin, tenantStore, store, ConnectRequest{Reference: "website/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.LinuxUser != "machineuser" {
		t.Fatalf("LinuxUser = %q", plan.LinuxUser)
	}
}

func TestPlanConnectResolvesMachineFQDN(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	store := fakeMachineStore{machines: []meta.Machine{
		{Project: "website", Name: "codex", PrivateIP: "10.248.0.42", TailscaleIP: "100.64.0.42"},
		{Project: "default", Name: "shell"},
	}}
	plan, err := PlanConnect(context.Background(), admin, tenantStoreForTest(t), store, ConnectRequest{Reference: "codex.website.acme"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project != "website" || plan.Name != "codex" || plan.InstanceName != "website-codex" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanConnectResolvesMachineFQDNWithTrailingDot(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	store := fakeMachineStore{machines: []meta.Machine{{Project: "website", Name: "codex", PrivateIP: "10.248.0.42", TailscaleIP: "100.64.0.42"}}}
	plan, err := PlanConnect(context.Background(), admin, tenantStoreForTest(t), store, ConnectRequest{Reference: "codex.website.acme."})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project != "website" || plan.Name != "codex" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanConnectConnectsUnmanagedReservedMachine(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	store := fakeMachineStore{unmanaged: []UnmanagedMachine{{Name: "sc-dns", InstanceName: "sc-dns"}}}
	plan, err := PlanConnect(context.Background(), admin, tenantStoreForTest(t), store, ConnectRequest{Reference: "sc-dns"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Managed || plan.InstanceName != "sc-dns" || plan.LinuxUser != "root" || plan.UserID != 0 || plan.GroupID != 0 || plan.WorkingDir != "/root" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanConnectRejectsAmbiguousBareMachine(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	store := fakeMachineStore{machines: []meta.Machine{{Project: "default", Name: "codex"}, {Project: "website", Name: "codex"}}}
	_, err := PlanConnect(context.Background(), admin, tenantStoreForTest(t), store, ConnectRequest{Reference: "codex"})
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

func TestMachineStatus(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	result, err := GetStatus(context.Background(), admin, tenantStoreForTest(t), fakeMachineStore{machines: []meta.Machine{{
		Tenant:    "acme",
		Project:   "default",
		Name:      "codex",
		PrivateIP: "10.248.0.20",
	}}}, StatusRequest{Reference: "codex"})
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

func tenantStoreForTest(t *testing.T) tenant.MemoryStore {
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
	return tenant.MemoryStore{Projects: []tenant.IncusProject{{Name: "sc-acme", Config: config}}}
}

type fakeMachineStore struct {
	machines  []meta.Machine
	unmanaged []UnmanagedMachine
}

func (s fakeMachineStore) ListMachines(ctx context.Context, summary tenant.Summary) ([]meta.Machine, error) {
	return s.machines, nil
}

func (s fakeMachineStore) ListUnmanagedMachines(ctx context.Context, summary tenant.Summary) ([]UnmanagedMachine, error) {
	return s.unmanaged, nil
}
