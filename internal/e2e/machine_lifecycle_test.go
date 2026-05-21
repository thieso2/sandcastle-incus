package e2e

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/images"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestMachineLifecycleE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}
	baseSource := strings.TrimSpace(e2eConfig.Images.BaseSource)
	aiSource := strings.TrimSpace(e2eConfig.Images.AISource)
	if baseSource == "" || aiSource == "" {
		t.Skip("set SANDCASTLE_E2E_BASE_IMAGE_SOURCE and SANDCASTLE_E2E_AI_IMAGE_SOURCE to already-imported Sandcastle image aliases")
	}

	ctx := context.Background()
	runID := e2eConfig.DisposableRunID()
	tenantName := safeTenantResourceName("tenant-" + runID)
	machineName := safeTenantResourceName("box-" + runID)
	ref := tenantName
	machineRef := machineName
	baseAlias := "sandcastle/base:" + safeToken(runID) + "-machine"
	aiAlias := "sandcastle/ai:" + safeToken(runID) + "-machine"
	adminConfig := config.Admin{
		Tenant:                ref,
		Remote:                e2eConfig.Remote,
		StoragePool:           e2eConfig.StoragePool,
		CIDRPool:              e2eConfig.CIDRPool,
		IncusProjectPrefix:    config.DefaultIncusProjectPrefix,
		InfrastructureProject: config.DefaultInfrastructureProject,
		Images: config.Images{
			Base: baseAlias,
			AI:   aiAlias,
		},
	}

	server, err := e2eInstanceServer(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupImageAlias(t, e2eConfig, server, aiAlias))
	t.Cleanup(cleanupImageAlias(t, e2eConfig, server, baseAlias))

	imageManager := incusx.NewImageManager(e2eConfig.Remote)
	syncImageAlias(t, ctx, imageManager, adminConfig, baseSource)
	syncImageAlias(t, ctx, imageManager, adminConfig, aiSource)

	store := incusx.NewTenantStore(e2eConfig.Remote)
	registerTenantDiagnostics(t, ctx, store, incusx.NewTopologyStore(e2eConfig.Remote), runID)
	creator := incusx.NewTenantCreator(e2eConfig.Remote)
	tenantDeleter := incusx.NewTenantDeleter(e2eConfig.Remote)
	deletePlan, err := tenant.PlanDelete(adminConfig, tenant.DeleteRequest{Reference: ref, Purge: true})
	if err != nil {
		t.Fatal(err)
	}
	// Pre-cleanup: remove any leaked project with the same name from a previous run.
	if err := tenantDeleter.DeleteTenant(ctx, deletePlan); err != nil {
		t.Logf("pre-cleanup for %s: %v", ref, err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable tenant %s", ref)
			return
		}
		if err := tenantDeleter.DeleteTenant(ctx, deletePlan); err != nil {
			t.Logf("cleanup failed for %s: %v", ref, err)
		}
	})

	existing, err := tenant.List(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	createTenantPlan, err := tenant.PlanCreate(adminConfig, tenant.CreateRequest{
		Reference:     ref,
		SSHPublicKey:  e2eConfig.SSHPublicKey,
		OccupiedCIDRs: tenant.OccupiedCIDRs(existing),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateTenant(ctx, createTenantPlan); err != nil {
		t.Fatal(err)
	}

	machineCreator := incusx.NewMachineCreator(e2eConfig.Remote)
	machineStore := incusx.NewHostOverrideManager(e2eConfig.Remote)
	createMachinePlan, err := machine.PlanCreate(ctx, adminConfig, store, machineStore, machine.CreateRequest{
		Reference: machineRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := machineCreator.CreateMachine(ctx, createMachinePlan); err != nil {
		t.Fatal(err)
	}

	projectServer := server.UseProject(createTenantPlan.IncusProject)
	assertInstanceExists(t, projectServer, createMachinePlan.InstanceName)
	hostname := machineName + ".default." + createTenantPlan.DNSSuffix
	assertMachineIngressFiles(t, projectServer, createMachinePlan.InstanceName, hostname, createMachinePlan.AppPort)
	startMachineHTTPApp(t, projectServer, createMachinePlan.InstanceName, createMachinePlan.AppPort, "sandcastle-app-3000")
	assertMachineCaddyProxy(t, projectServer, createMachinePlan.InstanceName, hostname, "sandcastle-app-3000")

	portPlan, err := machine.PlanSetPort(ctx, adminConfig, store, machine.PortSetRequest{
		Reference: machineRef,
		AppPort:   5173,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := incusx.NewMachinePortSetter(e2eConfig.Remote).SetAppPort(ctx, portPlan); err != nil {
		t.Fatal(err)
	}
	assertMachineIngressFiles(t, projectServer, createMachinePlan.InstanceName, hostname, 5173)
	startMachineHTTPApp(t, projectServer, createMachinePlan.InstanceName, 5173, "sandcastle-app-5173")
	assertMachineCaddyProxy(t, projectServer, createMachinePlan.InstanceName, hostname, "sandcastle-app-5173")

	controller := incusx.NewMachineController(e2eConfig.Remote)
	for _, action := range []machine.Action{machine.ActionStop, machine.ActionStart, machine.ActionRestart, machine.ActionDelete} {
		plan, err := machine.PlanLifecycle(ctx, adminConfig, store, machineStore, machine.LifecycleRequest{
			Reference: machineRef,
			Action:    action,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := controller.ApplyLifecycle(ctx, plan); err != nil {
			t.Fatalf("%s machine: %v", action, err)
		}
	}
	if _, _, err := projectServer.GetInstance(createMachinePlan.InstanceName); !api.StatusErrorCheck(err, http.StatusNotFound) {
		t.Fatalf("expected machine %s to be deleted, err = %v", createMachinePlan.InstanceName, err)
	}

	// After machine deletion the durable volumes must still exist (they are not purged on machine delete).
	for _, vol := range []string{tenant.HomeVolumeName, tenant.WorkspaceVolumeName, tenant.CAVolumeName} {
		if _, _, err := projectServer.GetStoragePoolVolume(createTenantPlan.StoragePool, "custom", vol); err != nil {
			t.Fatalf("expected durable volume %q to survive machine deletion: %v", vol, err)
		}
	}

	// Recreate the machine and verify it re-attaches to the same volumes.
	createMachinePlan2, err := machine.PlanCreate(ctx, adminConfig, store, incusx.NewHostOverrideManager(e2eConfig.Remote), machine.CreateRequest{
		Reference: machineRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := machineCreator.CreateMachine(ctx, createMachinePlan2); err != nil {
		t.Fatalf("recreate machine: %v", err)
	}
	assertInstanceExists(t, projectServer, createMachinePlan2.InstanceName)
	assertMachineIngressFiles(t, projectServer, createMachinePlan2.InstanceName, hostname, createMachinePlan2.AppPort)
}

func startMachineHTTPApp(t *testing.T, server interface {
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}, instance string, port int, body string) {
	t.Helper()
	command := []string{"/bin/sh", "-lc", fmt.Sprintf(
		"install -d /tmp/sandcastle-app-%d && printf %%s %s >/tmp/sandcastle-app-%d/index.html && cd /tmp/sandcastle-app-%d && nohup python3 -m http.server %d --bind 127.0.0.1 >/tmp/sandcastle-app-%d.log 2>&1 & for i in $(seq 1 50); do curl -fsS http://127.0.0.1:%d/ >/dev/null 2>&1 && exit 0; sleep 0.1; done; exit 1",
		port,
		shellQuote(body),
		port,
		port,
		port,
		port,
		port,
	)}
	_ = execInstanceOutput(t, server, instance, command)
}

func assertMachineCaddyProxy(t *testing.T, server interface {
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}, instance string, hostname string, want string) {
	t.Helper()
	output := execInstanceOutput(t, server, instance, []string{
		"curl", "-ksS", "--resolve", hostname + ":443:127.0.0.1", "https://" + hostname + "/",
	})
	if !strings.Contains(output, want) {
		t.Fatalf("machine Caddy proxy output = %q, want %q", output, want)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func syncImageAlias(t *testing.T, ctx context.Context, manager incusx.ImageManager, adminConfig config.Admin, source string) {
	t.Helper()
	plan, err := images.PlanSync(adminConfig, images.SyncRequest{SourceRef: source})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SyncImage(ctx, plan); err != nil {
		t.Fatal(err)
	}
}

func cleanupImageAlias(t *testing.T, e2eConfig Config, server interface {
	DeleteImageAlias(name string) error
}, alias string) func() {
	return func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable image alias %s", alias)
			return
		}
		if err := server.DeleteImageAlias(alias); err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			t.Logf("cleanup failed for image alias %s: %v", alias, err)
		}
	}
}
