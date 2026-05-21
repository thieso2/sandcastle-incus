package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/cli"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestCLIConnectCommandE2E(t *testing.T) {
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
	machineName := safeTenantResourceName("connect-" + runID)
	ref := tenantName
	machineRef := machineName
	baseAlias := "sandcastle/base:" + safeToken(runID) + "-connect"
	aiAlias := "sandcastle/ai:" + safeToken(runID) + "-connect"
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
		OccupiedCIDRs: tenant.OccupiedCIDRs(existing),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateTenant(ctx, createTenantPlan); err != nil {
		t.Fatal(err)
	}

	createMachinePlan, err := machine.PlanCreate(ctx, adminConfig, store, incusx.NewHostOverrideManager(e2eConfig.Remote), machine.CreateRequest{Reference: machineRef})
	if err != nil {
		t.Fatal(err)
	}
	if err := incusx.NewMachineCreator(e2eConfig.Remote).CreateMachine(ctx, createMachinePlan); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SANDCASTLE_REMOTE", e2eConfig.Remote)
	t.Setenv("SANDCASTLE_TENANT", ref)
	t.Setenv("SANDCASTLE_STORAGE_POOL", e2eConfig.StoragePool)
	t.Setenv("SANDCASTLE_CIDR_POOL", e2eConfig.CIDRPool)
	t.Setenv("SANDCASTLE_INCUS_PROJECT_PREFIX", config.DefaultIncusProjectPrefix)
	t.Setenv("SANDCASTLE_INFRA_PROJECT", config.DefaultInfrastructureProject)
	t.Setenv("SANDCASTLE_BASE_IMAGE", baseAlias)
	t.Setenv("SANDCASTLE_AI_IMAGE", aiAlias)
	if exitCode := cli.Execute("sandcastle", []string{"connect", machineRef, "pwd"}); exitCode != 0 {
		t.Fatalf("sandcastle connect pwd exit code = %d", exitCode)
	}
}
