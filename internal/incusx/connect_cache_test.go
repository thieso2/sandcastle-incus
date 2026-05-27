package incusx

import (
	"path/filepath"
	"testing"

	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestConnectCacheInvalidatePlansByNameExceptPrunesStaleNameMatches(t *testing.T) {
	cache := ConnectCache{path: filepath.Join(t.TempDir(), "connect-cache.json")}
	cache.StorePlan("thieso2:io/dev", machine.ConnectPlan{
		Tenant:    testConnectCacheTenant("thieso2"),
		Project:   "io",
		Name:      "dev",
		Hostname:  "dev.io.thieso2",
		PrivateIP: "10.248.1.27",
		Managed:   true,
	})
	cache.StorePlan("thieso2:7ed/dev", machine.ConnectPlan{
		Tenant:    testConnectCacheTenant("thieso2"),
		Project:   "7ed",
		Name:      "dev",
		Hostname:  "dev.7ed.thieso2",
		PrivateIP: "10.248.1.20",
		Managed:   true,
	})
	cache.MarkKeyscanned("dev.io.thieso2")
	cache.MarkKeyscanned("dev.7ed.thieso2")

	if _, ok := cache.LookupPlanByName("thieso2", "dev"); ok {
		t.Fatal("expected ambiguous name cache miss before pruning")
	}

	cache.InvalidatePlansByNameExcept("thieso2", "dev", "7ed")

	plan, ok := cache.LookupPlanByName("thieso2", "dev")
	if !ok {
		t.Fatal("expected unambiguous name cache hit after pruning")
	}
	if plan.Project != "7ed" || plan.Hostname != "dev.7ed.thieso2" {
		t.Fatalf("plan = %#v", plan)
	}
	if cache.IsKeyscanRecent("dev.io.thieso2") {
		t.Fatal("stale keyscan was not removed")
	}
	if !cache.IsKeyscanRecent("dev.7ed.thieso2") {
		t.Fatal("kept keyscan was removed")
	}
}

func testConnectCacheTenant(name string) tenant.Summary {
	return tenant.Summary{Tenant: name}
}
