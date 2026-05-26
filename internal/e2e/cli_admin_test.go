package e2e

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

// TestCLIAdminTenantCreateE2E verifies that the sc admin tenant create / delete commands
// work end-to-end, including the admin remote detection that uses the global ~/.config/incus/
// config (admin TLS certificates) rather than the per-user Sandcastle config directory.
func TestCLIAdminTenantCreateE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}

	sandcastleBin := strings.TrimSpace(e2eConfig.SandcastleBin)
	if sandcastleBin == "" {
		sandcastleBin = buildSandcastleAdminForE2E(t)
	}

	runID := e2eConfig.DisposableRunID()
	ref := safeTenantResourceName("cadmin-" + runID)

	adminConfig := config.Admin{
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
	ctx := context.Background()
	deletePlan, err := tenant.PlanDelete(adminConfig, tenant.DeleteRequest{Reference: ref, Purge: true})
	if err != nil {
		t.Fatal(err)
	}
	deleter := incusx.NewTenantDeleter(e2eConfig.Remote)

	// Pre-cleanup: remove any leaked project from a previous run.
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

	// Derive the Incus project name from the plan so we can check it afterwards.
	// We call PlanCreate with zero OccupiedCIDRs just to get the name — the CLI will
	// do its own plan with the real list.
	createPlan, err := tenant.PlanCreate(adminConfig, tenant.CreateRequest{
		Reference: ref,
	})
	if err != nil {
		t.Fatal(err)
	}

	server, err := e2eInstanceServer(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}

	// Run: sc-adm tenant create via the native admin CLI binary.
	runAdminCLI(t, e2eConfig, sandcastleBin, 5*time.Minute,
		"tenant", "create", ref)

	// Verify the Incus project was created.
	if _, _, err := server.GetProject(createPlan.IncusProject); err != nil {
		t.Fatalf("expected Incus project %s to exist after sc admin tenant create: %v", createPlan.IncusProject, err)
	}

	// Run: sc-adm tenant delete --yes --purge.
	runAdminCLI(t, e2eConfig, sandcastleBin, 5*time.Minute,
		"tenant", "delete", ref, "--yes", "--purge")

	// Verify the Incus project is gone.
	if _, _, err := server.GetProject(createPlan.IncusProject); !api.StatusErrorCheck(err, http.StatusNotFound) {
		t.Fatalf("expected Incus project %s to be deleted, err = %v", createPlan.IncusProject, err)
	}
}

// runAdminCLI runs the sandcastle binary with admin-appropriate env (no INCUS_CONF so
// admin commands use ~/.config/incus/ with admin TLS certificates).
func runAdminCLI(t *testing.T, e2eConfig Config, bin string, timeout time.Duration, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = adminCLIEnv(e2eConfig)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("sc %s: %v", strings.Join(args, " "), err)
	}
}

// adminCLIEnv builds the environment for sc admin commands: SANDCASTLE_* vars pointing at
// the admin Incus remote, with INCUS_CONF stripped so the global ~/.config/incus/ is used.
func adminCLIEnv(e2eConfig Config) []string {
	// Start from the current process environment but remove INCUS_CONF — admin commands
	// must use ~/.config/incus/ (admin certificates), not a per-user config directory.
	filtered := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "INCUS_CONF=") {
			filtered = append(filtered, e)
		}
	}
	return append(filtered,
		"SANDCASTLE_REMOTE="+e2eConfig.Remote,
		"SANDCASTLE_STORAGE_POOL="+e2eConfig.StoragePool,
		"SANDCASTLE_CIDR_POOL="+e2eConfig.CIDRPool,
		"SANDCASTLE_INCUS_PROJECT_PREFIX="+config.DefaultIncusProjectPrefix,
		"SANDCASTLE_INFRA_PROJECT="+config.DefaultInfrastructureProject,
	)
}
