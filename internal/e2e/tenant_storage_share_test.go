package e2e

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/share"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestTenantStorageShareReadOnlyE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}
	if !e2eConfig.LocalVM {
		t.Skip("set SANDCASTLE_E2E_LOCAL_VM=1 to run tests that require direct machine access")
	}
	baseSource := strings.TrimSpace(e2eConfig.Images.BaseSource)
	aiSource := strings.TrimSpace(e2eConfig.Images.AISource)
	if baseSource == "" || aiSource == "" {
		t.Skip("set SANDCASTLE_E2E_BASE_IMAGE_SOURCE and SANDCASTLE_E2E_AI_IMAGE_SOURCE to already-imported Sandcastle image aliases")
	}

	ctx := context.Background()
	runID := e2eConfig.DisposableRunID()
	sourceTenant := safeTenantResourceName("share-src-" + runID)
	recipientTenant := safeTenantResourceName("share-rec-" + runID)
	sourceMachine := safeTenantResourceName("src-" + runID)
	recipientMachine := safeTenantResourceName("rec-" + runID)
	baseAlias := "sandcastle/base:" + safeToken(runID) + "-share"
	aiAlias := "sandcastle/ai:" + safeToken(runID) + "-share"
	adminConfig := config.Admin{
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
	sourceAdmin := adminConfig
	sourceAdmin.Tenant = sourceTenant
	syncImageAlias(t, ctx, imageManager, sourceAdmin, baseSource)
	syncImageAlias(t, ctx, imageManager, sourceAdmin, aiSource)

	store := incusx.NewTenantStore(e2eConfig.Remote)
	topologyStore := incusx.NewTopologyStore(e2eConfig.Remote)
	registerTenantDiagnostics(t, ctx, store, topologyStore, runID)
	creator := incusx.NewTenantCreator(e2eConfig.Remote)
	deleter := incusx.NewTenantDeleter(e2eConfig.Remote)
	sourcePlan := createShareE2ETenant(t, ctx, e2eConfig, adminConfig, store, creator, deleter, sourceTenant)
	recipientPlan := createShareE2ETenant(t, ctx, e2eConfig, adminConfig, store, creator, deleter, recipientTenant)

	machineCreator := incusx.NewMachineCreator(e2eConfig.Remote)
	machineStore := incusx.NewHostOverrideManager(e2eConfig.Remote)
	sourceMachinePlan := createShareE2EMachine(t, ctx, sourceAdmin, store, machineStore, machineCreator, sourceMachine)
	recipientAdmin := adminConfig
	recipientAdmin.Tenant = recipientTenant
	recipientMachinePlan := createShareE2EMachine(t, ctx, recipientAdmin, store, machineStore, machineCreator, recipientMachine)
	sourceServer := server.UseProject(sourcePlan.IncusProject)
	recipientServer := server.UseProject(recipientPlan.IncusProject)

	execInstanceOutput(t, sourceServer, sourceMachinePlan.InstanceName, []string{
		"/bin/sh", "-lc", "install -d /workspace/docs && printf 'source-one\\n' >/workspace/docs/marker.txt",
	})

	shareStore := incusx.NewTenantSSHKeyManager(e2eConfig.Remote)
	if _, err := share.PlanCreate(ctx, store, shareStore, share.CreateRequest{
		SourceTenant: sourceTenant,
		Source:       "default:/workspace/docs",
		Recipients:   []string{recipientTenant},
		Actor:        "e2e",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := share.SetRecipientState(ctx, store, shareStore, share.RecipientRequest{
		Tenant:        recipientTenant,
		SourceTenant:  sourceTenant,
		SourceProject: "default",
		Name:          "docs",
		Actor:         "e2e",
		State:         share.RecipientStateAccepted,
	}); err != nil {
		t.Fatal(err)
	}

	recipientSummary := shareE2ETenantSummary(t, ctx, store, recipientTenant)
	reconciler := incusx.NewShareReconciler(e2eConfig.Remote, machineStore)
	reconciler.Admin = adminConfig
	reconcileResult, err := reconciler.ReconcileTenantShares(ctx, recipientSummary, false)
	if err != nil {
		t.Fatal(err)
	}
	if reconcileResult.HasFailures() {
		t.Fatalf("reconcile failed: %#v", reconcileResult)
	}

	sharePath := "/shared/" + sourceTenant + "/default/docs"
	marker := strings.TrimSpace(execInstanceOutput(t, recipientServer, recipientMachinePlan.InstanceName, []string{
		"cat", sharePath + "/marker.txt",
	}))
	if marker != "source-one" {
		t.Fatalf("recipient marker = %q, want source-one", marker)
	}
	execInstanceOutput(t, sourceServer, sourceMachinePlan.InstanceName, []string{
		"/bin/sh", "-lc", "printf 'source-two\\n' >/workspace/docs/second.txt",
	})
	second := strings.TrimSpace(execInstanceOutput(t, recipientServer, recipientMachinePlan.InstanceName, []string{
		"cat", sharePath + "/second.txt",
	}))
	if second != "source-two" {
		t.Fatalf("recipient second marker = %q, want source-two", second)
	}
	if code, _, _ := execInstance(t, recipientServer, recipientMachinePlan.InstanceName, []string{
		"/bin/sh", "-lc", "printf denied >" + shellQuote(sharePath+"/recipient-write.txt"),
	}); code == 0 {
		t.Fatalf("recipient write to read-only share unexpectedly succeeded")
	}

	if _, err := share.DeleteOutbound(ctx, store, shareStore, share.DeleteRequest{
		SourceTenant:  sourceTenant,
		SourceProject: "default",
		Name:          "docs",
	}); err != nil {
		t.Fatal(err)
	}
	recipientSummary = shareE2ETenantSummary(t, ctx, store, recipientTenant)
	reconcileResult, err = reconciler.ReconcileTenantShares(ctx, recipientSummary, false)
	if err != nil {
		t.Fatal(err)
	}
	if reconcileResult.HasFailures() {
		t.Fatalf("delete reconcile failed: %#v", reconcileResult)
	}
	if code, _, _ := execInstance(t, recipientServer, recipientMachinePlan.InstanceName, []string{
		"test", "-e", sharePath,
	}); code == 0 {
		t.Fatalf("recipient share path still exists after delete/reconcile")
	}
}

func createShareE2ETenant(t *testing.T, ctx context.Context, e2eConfig Config, adminConfig config.Admin, store tenant.IncusTenantStore, creator tenant.Creator, deleter tenant.Deleter, tenantName string) tenant.CreatePlan {
	t.Helper()
	admin := adminConfig
	admin.Tenant = tenantName
	deletePlan, err := tenant.PlanDelete(admin, tenant.DeleteRequest{Reference: tenantName, Purge: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := deleter.DeleteTenant(ctx, deletePlan); err != nil {
		t.Logf("pre-cleanup for %s: %v", tenantName, err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable tenant %s", tenantName)
			return
		}
		if err := deleter.DeleteTenant(ctx, deletePlan); err != nil {
			t.Logf("cleanup failed for %s: %v", tenantName, err)
		}
	})
	existing, err := tenant.List(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := tenant.PlanCreate(admin, tenant.CreateRequest{
		Reference:     tenantName,
		SSHPublicKey:  e2eConfig.SSHPublicKey,
		OccupiedCIDRs: tenant.OccupiedCIDRs(existing),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateTenant(ctx, plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func createShareE2EMachine(t *testing.T, ctx context.Context, admin config.Admin, tenantStore tenant.IncusTenantStore, machineStore machine.Store, creator machine.Creator, machineName string) machine.CreatePlan {
	t.Helper()
	plan, err := machine.PlanCreate(ctx, admin, tenantStore, machineStore, machine.CreateRequest{
		Reference: machineName,
		Template:  machine.TemplateBase,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateMachine(ctx, plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func shareE2ETenantSummary(t *testing.T, ctx context.Context, store tenant.IncusTenantStore, tenantName string) tenant.Summary {
	t.Helper()
	summaries, err := tenant.List(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range summaries {
		if summary.Tenant == tenantName {
			return summary
		}
	}
	t.Fatalf("tenant %s not found", tenantName)
	return tenant.Summary{}
}

func execInstance(t *testing.T, server instanceExecServer, instance string, command []string) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	done := make(chan bool)
	op, err := server.ExecInstance(instance, api.InstanceExecPost{
		Command:   command,
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdout:   &stdout,
		Stderr:   &stderr,
		DataDone: done,
	})
	if err != nil {
		t.Fatalf("exec %s in %s: %v", strings.Join(command, " "), instance, err)
	}
	waitErr := op.Wait()
	<-done
	if waitErr != nil {
		statusCode := operationStatusCode(op.Get())
		if statusCode != 0 {
			return statusCode, stdout.String(), stderr.String()
		}
		return 1, stdout.String(), stderr.String()
	}
	return 0, stdout.String(), stderr.String()
}

func operationStatusCode(operation api.Operation) int {
	if operation.Metadata == nil {
		return 0
	}
	for _, key := range []string{"return", "exit_code"} {
		value, ok := operation.Metadata[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int:
			return typed
		case int64:
			return int(typed)
		case float64:
			return int(typed)
		case string:
			var parsed int
			if _, err := fmt.Sscanf(typed, "%d", &parsed); err == nil {
				return parsed
			}
		}
	}
	return 0
}
