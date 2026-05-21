package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestDisposableTenantCreateAndPurge(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	runID := e2eConfig.DisposableRunID()
	tenantName := safeTenantResourceName("tenant-" + runID)
	ref := tenantName
	adminConfig := config.Admin{
		Tenant:                ref,
		Remote:                e2eConfig.Remote,
		StoragePool:           e2eConfig.StoragePool,
		CIDRPool:              e2eConfig.CIDRPool,
		IncusProjectPrefix:    config.DefaultIncusProjectPrefix,
		InfrastructureProject: config.DefaultInfrastructureProject,
		Images: config.Images{
			Base: config.DefaultBaseImageAlias,
			AI:   config.DefaultAIImageAlias,
		},
	}
	store := incusx.NewTenantStore(e2eConfig.Remote)
	topologyStore := incusx.NewTopologyStore(e2eConfig.Remote)
	creator := incusx.NewTenantCreator(e2eConfig.Remote)
	deleter := incusx.NewTenantDeleter(e2eConfig.Remote)
	registerTenantDiagnostics(t, ctx, store, topologyStore, runID)

	deletePlan, err := tenant.PlanDelete(adminConfig, tenant.DeleteRequest{Reference: ref, Purge: true})
	if err != nil {
		t.Fatal(err)
	}
	// Pre-cleanup: remove any leaked project with the same name from a previous run.
	if err := deleter.DeleteTenant(ctx, deletePlan); err != nil {
		t.Logf("pre-cleanup for %s: %v", ref, err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable tenant %s", ref)
			return
		}
		if err := deleter.DeleteTenant(ctx, deletePlan); err != nil {
			t.Logf("cleanup failed for %s: %v", ref, err)
		}
	})

	existing, err := tenant.List(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	createPlan, err := tenant.PlanCreate(adminConfig, tenant.CreateRequest{
		Reference:     ref,
		SSHPublicKey:  e2eConfig.SSHPublicKey,
		OccupiedCIDRs: tenant.OccupiedCIDRs(existing),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateTenant(ctx, createPlan); err != nil {
		t.Fatal(err)
	}

	// Verify tenant appears in listing.
	afterCreate, err := tenant.List(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if !containsTenant(afterCreate, tenantName) {
		t.Fatalf("created tenant %s was not listed in %#v", ref, afterCreate)
	}

	// Verify per-tenant storage pool was created.
	server, err := e2eInstanceServer(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}
	pool, _, err := server.GetStoragePool(createPlan.StoragePool)
	if err != nil {
		t.Fatalf("expected per-tenant storage pool %q to exist: %v", createPlan.StoragePool, err)
	}
	if pool.Driver != "zfs" && pool.Driver != "btrfs" && pool.Driver != "dir" {
		t.Logf("storage pool driver = %q (expected zfs for production)", pool.Driver)
	}

	// Verify per-tenant container profile was created with the right devices.
	projectServer := server.UseProject(createPlan.IncusProject)
	profile, _, err := projectServer.GetProfile("container")
	if err != nil {
		t.Fatalf("expected container profile in project %q: %v", createPlan.IncusProject, err)
	}
	rootDevice, ok := profile.Devices["root"]
	if !ok {
		t.Fatalf("container profile has no root device; devices = %v", profile.Devices)
	}
	if rootDevice["pool"] != createPlan.StoragePool {
		t.Fatalf("container profile root pool = %q, want %q", rootDevice["pool"], createPlan.StoragePool)
	}
	if rootDevice["type"] != "disk" {
		t.Fatalf("container profile root type = %q, want disk", rootDevice["type"])
	}

	// Verify idempotent create — calling CreateProject a second time must not fail.
	if err := creator.CreateTenant(ctx, createPlan); err != nil {
		t.Fatalf("idempotent tenant create failed: %v", err)
	}

	// Purge and confirm all resources are gone.
	if err := deleter.DeleteTenant(ctx, deletePlan); err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.GetStoragePool(createPlan.StoragePool); !api.StatusErrorCheck(err, http.StatusNotFound) {
		t.Fatalf("expected storage pool %q to be deleted after purge, err = %v", createPlan.StoragePool, err)
	}
}

func safeTenantResourceName(value string) string {
	value = safeToken(value)
	if len(value) > 27 {
		value = value[:27]
	}
	return strings.Trim(value, "-")
}

func containsTenant(tenants []tenant.Summary, tenantName string) bool {
	for _, summary := range tenants {
		if summary.Tenant == tenantName {
			return true
		}
	}
	return false
}
