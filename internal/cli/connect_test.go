package cli

import (
	"fmt"
	"os/exec"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/incusx"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestShouldRetryCachedConnectFailureOnlyForSSHTransportExit(t *testing.T) {
	if !shouldRetryCachedConnectFailure(wrappedExitError(t, 255)) {
		t.Fatal("exit 255 should retry cached connect")
	}
	for _, code := range []int{0, 1, 2, 130} {
		if shouldRetryCachedConnectFailure(wrappedExitError(t, code)) {
			t.Fatalf("exit %d should not retry cached connect", code)
		}
	}
	if shouldRetryCachedConnectFailure(fmt.Errorf("ssh to machine: command failed")) {
		t.Fatal("non-exit errors should not retry cached connect")
	}
}

func TestPruneBareNameConnectCacheRemovesStaleNameMatches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cache := incusx.NewConnectCache("sandcastle-test")
	stale := machine.ConnectPlan{
		Tenant:    tenant.Summary{Tenant: "thieso2"},
		Project:   "io",
		Name:      "dev",
		Hostname:  "dev.io.thieso2",
		PrivateIP: "10.248.1.27",
		Managed:   true,
	}
	kept := machine.ConnectPlan{
		Tenant:    tenant.Summary{Tenant: "thieso2"},
		Project:   "7ed",
		Name:      "dev",
		Hostname:  "dev.7ed.thieso2",
		PrivateIP: "10.248.1.20",
		Managed:   true,
	}
	cache.StorePlan("thieso2:io/dev", stale)
	cache.StorePlan("thieso2:7ed/dev", kept)

	pruneBareNameConnectCache(cache, commandConfig{adminConfig: testAdminConfig()}, "dev", kept)

	plan, ok := cache.LookupPlanByName("thieso2", "dev")
	if !ok {
		t.Fatal("expected name cache hit")
	}
	if plan.Project != "7ed" {
		t.Fatalf("project = %q, want 7ed", plan.Project)
	}
	if _, ok := cache.LookupPlan("thieso2:io/dev"); ok {
		t.Fatal("stale project plan still cached")
	}
}

func TestPruneBareNameConnectCachePreservesExplicitDefaultProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cache := incusx.NewConnectCache("sandcastle-test")
	stale := machine.ConnectPlan{
		Tenant:  tenant.Summary{Tenant: "thieso2"},
		Project: "io",
		Name:    "dev",
		Managed: true,
	}
	kept := machine.ConnectPlan{
		Tenant:  tenant.Summary{Tenant: "thieso2"},
		Project: "7ed",
		Name:    "dev",
		Managed: true,
	}
	cache.StorePlan("thieso2:io/dev", stale)
	cache.StorePlan("thieso2:7ed/dev", kept)
	admin := testAdminConfig()
	admin.Project = "7ed"

	pruneBareNameConnectCache(cache, commandConfig{adminConfig: admin}, "dev", kept)

	if _, ok := cache.LookupPlan("thieso2:io/dev"); !ok {
		t.Fatal("explicit project should not prune same-name plans")
	}
}

func wrappedExitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	if code == 0 {
		return nil
	}
	if err == nil {
		t.Fatalf("exit %d returned nil error", code)
	}
	return fmt.Errorf("ssh to machine default-test: %w", err)
}
