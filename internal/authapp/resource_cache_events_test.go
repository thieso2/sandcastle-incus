package authapp

import (
	"testing"

	"github.com/lxc/incus/v6/shared/api"
)

// seededCache returns a cache and fake server already past the initial read,
// with an empty "acme" project, ready for event-driven mutation tests.
func seededCache(t *testing.T) (*ResourceCache, *fakeResourceCacheServer) {
	t.Helper()
	server := newFakeResourceCacheServer()
	server.pools = []api.StoragePool{{Name: "default"}}
	server.project("acme") // ensure the project exists with empty buckets
	cache := NewResourceCache(0)
	if err := seedResourceCache(cache, server, nil); err != nil {
		t.Fatalf("seedResourceCache: %v", err)
	}
	return cache, server
}

func TestHandleResourceCacheEvent_InstanceCreatedUpdatedDeletedRenamed(t *testing.T) {
	cache, server := seededCache(t)

	// Created: the live side now has one instance; the event should cause the
	// cache to pick it up even though the event itself carries no resource
	// details beyond action + project.
	server.project("acme").instances = []api.InstanceFull{{Instance: api.Instance{Project: "acme", Name: "web-1"}}}
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleInstanceCreated, "acme"), nil)
	if got := cache.Snapshot().Instances; len(got) != 1 || got[0].Name != "web-1" {
		t.Fatalf("after created: instances = %+v", got)
	}

	// Updated (e.g. a NIC/IP change) — this is the action the DNS reconciler's
	// trimmed set deliberately excludes but this cache cannot.
	server.project("acme").instances[0].State = &api.InstanceState{Network: map[string]api.InstanceStateNetwork{}}
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleInstanceUpdated, "acme"), nil)
	if got := cache.Snapshot().Instances; len(got) != 1 || got[0].State == nil {
		t.Fatalf("after updated: instances = %+v", got)
	}

	// Renamed: modeled as "the live list now shows the new name" — the cache
	// keys refresh by project only, so it never has to interpret whether the
	// event's own Name field is the old or new name.
	server.project("acme").instances[0].Name = "web-2"
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleInstanceRenamed, "acme"), nil)
	if got := cache.Snapshot().Instances; len(got) != 1 || got[0].Name != "web-2" {
		t.Fatalf("after renamed: instances = %+v", got)
	}

	// Deleted: the live side now has nothing.
	server.project("acme").instances = nil
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleInstanceDeleted, "acme"), nil)
	if got := cache.Snapshot().Instances; len(got) != 0 {
		t.Fatalf("after deleted: instances = %+v", got)
	}
}

func TestHandleResourceCacheEvent_NetworkLifecycle(t *testing.T) {
	cache, server := seededCache(t)
	server.project("acme").networks = []api.Network{{Name: "br0", Project: "acme"}}
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleNetworkCreated, "acme"), nil)
	if got := cache.Snapshot().Networks; len(got) != 1 || got[0].Name != "br0" {
		t.Fatalf("after created: networks = %+v", got)
	}

	server.project("acme").networks[0].Name = "br1"
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleNetworkRenamed, "acme"), nil)
	if got := cache.Snapshot().Networks; len(got) != 1 || got[0].Name != "br1" {
		t.Fatalf("after renamed: networks = %+v", got)
	}

	server.project("acme").networks = nil
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleNetworkDeleted, "acme"), nil)
	if got := cache.Snapshot().Networks; len(got) != 0 {
		t.Fatalf("after deleted: networks = %+v", got)
	}
}

func TestHandleResourceCacheEvent_StorageVolumeLifecycle(t *testing.T) {
	cache, server := seededCache(t)
	server.project("acme").volumesByPool["default"] = []api.StorageVolumeFull{{StorageVolume: api.StorageVolume{Name: "vol-1", Project: "acme"}}}
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleStorageVolumeCreated, "acme"), nil)
	if got := cache.Snapshot().StorageVolumes; len(got) != 1 || got[0].Name != "vol-1" {
		t.Fatalf("after created: volumes = %+v", got)
	}

	server.project("acme").volumesByPool["default"][0].Name = "vol-2"
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleStorageVolumeRenamed, "acme"), nil)
	if got := cache.Snapshot().StorageVolumes; len(got) != 1 || got[0].Name != "vol-2" {
		t.Fatalf("after renamed: volumes = %+v", got)
	}

	server.project("acme").volumesByPool["default"] = nil
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleStorageVolumeDeleted, "acme"), nil)
	if got := cache.Snapshot().StorageVolumes; len(got) != 0 {
		t.Fatalf("after deleted: volumes = %+v", got)
	}
}

func TestHandleResourceCacheEvent_StoragePoolLifecycleIsGlobalNotPerProject(t *testing.T) {
	cache, server := seededCache(t)
	server.pools = append(server.pools, api.StoragePool{Name: "fast"})
	// Storage pools are server-scoped, not project-scoped, so this event
	// carries no meaningful project — the refresh must still work.
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleStoragePoolCreated, ""), nil)
	got := cache.Snapshot().StoragePools
	if len(got) != 2 || got[0].Name != "default" || got[1].Name != "fast" {
		t.Fatalf("storage pools = %+v", got)
	}

	server.pools = server.pools[:1]
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleStoragePoolDeleted, ""), nil)
	got = cache.Snapshot().StoragePools
	if len(got) != 1 || got[0].Name != "default" {
		t.Fatalf("storage pools after delete = %+v", got)
	}
}

func TestHandleResourceCacheEvent_ProfileLifecycle(t *testing.T) {
	cache, server := seededCache(t)
	server.project("acme").profiles = []api.Profile{{Name: "gpu", Project: "acme"}}
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleProfileCreated, "acme"), nil)
	if got := cache.Snapshot().Profiles; len(got) != 1 || got[0].Name != "gpu" {
		t.Fatalf("after created: profiles = %+v", got)
	}

	server.project("acme").profiles = nil
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleProfileDeleted, "acme"), nil)
	if got := cache.Snapshot().Profiles; len(got) != 0 {
		t.Fatalf("after deleted: profiles = %+v", got)
	}
}

func TestHandleResourceCacheEvent_ImageLifecycleIncludingAliasEvents(t *testing.T) {
	cache, server := seededCache(t)
	server.project("acme").images = []api.Image{{Project: "acme", Fingerprint: "aaa"}}
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleImageCreated, "acme"), nil)
	if got := cache.Snapshot().Images; len(got) != 1 || got[0].Fingerprint != "aaa" {
		t.Fatalf("after created: images = %+v", got)
	}

	// image-alias-created changes api.Image.Aliases without a matching
	// image-updated event — the cache must still refresh on it.
	server.project("acme").images[0].Aliases = []api.ImageAlias{{Name: "base"}}
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleImageAliasCreated, "acme"), nil)
	if got := cache.Snapshot().Images; len(got) != 1 || len(got[0].Aliases) != 1 {
		t.Fatalf("after alias created: images = %+v", got)
	}

	server.project("acme").images = nil
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleImageDeleted, "acme"), nil)
	if got := cache.Snapshot().Images; len(got) != 0 {
		t.Fatalf("after deleted: images = %+v", got)
	}
}

func TestHandleResourceCacheEvent_IgnoresIrrelevantActions(t *testing.T) {
	cache, server := seededCache(t)
	server.project("acme").instances = []api.InstanceFull{{Instance: api.Instance{Project: "acme", Name: "web-1"}}}
	before := cache.Snapshot()

	// instance-exec and instance-file-retrieved are exactly the noisy actions
	// the DNS reconciler excludes for CPU reasons; they change nothing this
	// cache shows either, so they must not trigger a refresh.
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleInstanceExec, "acme"), nil)
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleInstanceFileRetrieved, "acme"), nil)

	after := cache.Snapshot()
	if len(after.Instances) != len(before.Instances) {
		t.Fatalf("an irrelevant action mutated the cache: before=%+v after=%+v", before.Instances, after.Instances)
	}
	if len(after.Instances) != 0 {
		t.Fatalf("expected no refresh to have happened: %+v", after.Instances)
	}
}

func TestHandleResourceCacheEvent_RefreshErrorIsLoggedAndCacheKeepsStaleData(t *testing.T) {
	cache, server := seededCache(t)
	server.project("acme").instances = []api.InstanceFull{{Instance: api.Instance{Project: "acme", Name: "web-1"}}}
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleInstanceCreated, "acme"), nil)
	if got := cache.Snapshot().Instances; len(got) != 1 {
		t.Fatalf("setup: instances = %+v", got)
	}

	server.err = errFakeResourceCacheServer
	var logged string
	handleResourceCacheEvent(cache, server, lifecycleEvent(api.EventLifecycleInstanceUpdated, "acme"), func(format string, args ...any) {
		logged = format
	})
	if logged == "" {
		t.Fatal("expected the refresh failure to be logged")
	}
	if got := cache.Snapshot().Instances; len(got) != 1 || got[0].Name != "web-1" {
		t.Fatalf("a failed refresh must not wipe the previously cached data: %+v", got)
	}
}
