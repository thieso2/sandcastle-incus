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

func TestConnectCacheStoresSSHIdentity(t *testing.T) {
	cache := ConnectCache{path: filepath.Join(t.TempDir(), "connect-cache.json")}
	cache.StoreSSHIdentity("thieso2:default/test", "/Users/thies/.ssh/id_ed25519")

	identityPath, ok := cache.LookupSSHIdentity("thieso2:default/test")
	if !ok {
		t.Fatal("expected SSH identity cache hit")
	}
	if identityPath != "/Users/thies/.ssh/id_ed25519" {
		t.Fatalf("identityPath = %q", identityPath)
	}

	cache.InvalidatePlan("thieso2:default/test")
	if _, ok := cache.LookupSSHIdentity("thieso2:default/test"); ok {
		t.Fatal("expected plan invalidation to remove SSH identity")
	}
}

func TestConnectCacheInvalidateTenantRemovesSSHIdentities(t *testing.T) {
	cache := ConnectCache{path: filepath.Join(t.TempDir(), "connect-cache.json")}
	cache.StoreSSHIdentity("thieso2:default/test", "/Users/thies/.ssh/id_ed25519")
	cache.StoreSSHIdentity("some:default/test", "/Users/thies/.ssh/id_ed25519")

	cache.InvalidateTenant("thieso2")
	if _, ok := cache.LookupSSHIdentity("thieso2:default/test"); ok {
		t.Fatal("expected tenant invalidation to remove SSH identity")
	}
	if _, ok := cache.LookupSSHIdentity("some:default/test"); !ok {
		t.Fatal("expected other tenant SSH identity to remain")
	}
}

func testConnectCacheTenant(name string) tenant.Summary {
	return tenant.Summary{Tenant: name}
}
