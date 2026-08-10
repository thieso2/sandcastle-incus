package authapp

import (
	"testing"
	"time"

	"github.com/lxc/incus/v6/shared/api"
)

func TestResourceCache_SeedBuildsIndexAcrossAllFiveResourceTypes(t *testing.T) {
	server := newFakeResourceCacheServer()
	server.pools = []api.StoragePool{{Name: "default"}}

	web := server.project("web")
	web.instances = []api.InstanceFull{{Instance: api.Instance{InstancePut: api.InstancePut{}, Project: "web", Name: "app-1"}}}
	web.networks = []api.Network{{Name: "br0", Project: "web"}}
	web.volumesByPool["default"] = []api.StorageVolumeFull{{StorageVolume: api.StorageVolume{Name: "app-1-root", Project: "web"}}}
	web.profiles = []api.Profile{{Name: "default", Project: "web"}}
	web.images = []api.Image{{Project: "web", Fingerprint: "abc123"}}

	worker := server.project("worker")
	worker.instances = []api.InstanceFull{{Instance: api.Instance{Project: "worker", Name: "job-1"}}}

	cache := NewResourceCache(0)
	if err := seedResourceCache(cache, server); err != nil {
		t.Fatalf("seedResourceCache: %v", err)
	}

	snapshot := cache.Snapshot()
	if len(snapshot.Instances) != 2 {
		t.Fatalf("instances = %d, want 2: %+v", len(snapshot.Instances), snapshot.Instances)
	}
	if snapshot.Instances[0].Project != "web" || snapshot.Instances[0].Name != "app-1" {
		t.Fatalf("instances[0] = %+v", snapshot.Instances[0])
	}
	if snapshot.Instances[1].Project != "worker" || snapshot.Instances[1].Name != "job-1" {
		t.Fatalf("instances[1] = %+v", snapshot.Instances[1])
	}
	if len(snapshot.Networks) != 1 || snapshot.Networks[0].Name != "br0" {
		t.Fatalf("networks = %+v", snapshot.Networks)
	}
	if len(snapshot.StorageVolumes) != 1 || snapshot.StorageVolumes[0].Name != "app-1-root" {
		t.Fatalf("storage volumes = %+v", snapshot.StorageVolumes)
	}
	if len(snapshot.StoragePools) != 1 || snapshot.StoragePools[0].Name != "default" {
		t.Fatalf("storage pools = %+v", snapshot.StoragePools)
	}
	if len(snapshot.Profiles) != 1 || snapshot.Profiles[0].Name != "default" {
		t.Fatalf("profiles = %+v", snapshot.Profiles)
	}
	if len(snapshot.Images) != 1 || snapshot.Images[0].Fingerprint != "abc123" {
		t.Fatalf("images = %+v", snapshot.Images)
	}

	// The initial read alone is not enough for Ready(): the event stream must
	// also be connected.
	if cache.Ready() {
		t.Fatal("cache reported ready before the event stream connected")
	}
	if snapshot.Ready {
		t.Fatal("snapshot reported ready before the event stream connected")
	}
}

func TestResourceCache_SeedFailurePropagatesAndLeavesCacheEmpty(t *testing.T) {
	server := newFakeResourceCacheServer()
	server.err = errFakeResourceCacheServer

	cache := NewResourceCache(0)
	if err := seedResourceCache(cache, server); err == nil {
		t.Fatal("expected seedResourceCache to return the underlying error")
	}
	if cache.Ready() {
		t.Fatal("cache reported ready after a failed seed")
	}
}

func TestResourceCache_ReadinessTransitions(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cache := NewResourceCache(time.Minute)
	cache.now = func() time.Time { return now }

	if cache.Ready() {
		t.Fatal("a freshly constructed cache must not be ready")
	}

	// Initial read done, but stream not yet connected: still not ready.
	cache.seed(nil, nil, nil, nil, nil, nil)
	if cache.Ready() {
		t.Fatal("cache became ready without the event stream connecting")
	}

	// Stream connects: now ready.
	cache.markStreamConnected()
	if !cache.Ready() {
		t.Fatal("cache did not become ready once seeded and connected")
	}

	// Time passes within the staleness window with no events: still ready.
	now = now.Add(30 * time.Second)
	if !cache.Ready() {
		t.Fatal("cache went not-ready before the staleness timeout elapsed")
	}

	// An event resets the heartbeat clock.
	cache.recordHeartbeat()
	now = now.Add(59 * time.Second)
	if !cache.Ready() {
		t.Fatal("cache went not-ready even though a recent heartbeat should have reset the clock")
	}

	// No more events; the staleness window elapses: not ready.
	now = now.Add(2 * time.Second)
	if cache.Ready() {
		t.Fatal("cache stayed ready past the staleness timeout with no heartbeat")
	}

	// A fresh heartbeat recovers readiness without needing to reconnect.
	cache.recordHeartbeat()
	if !cache.Ready() {
		t.Fatal("cache did not recover readiness after a fresh heartbeat")
	}

	// An explicit disconnect flips to not-ready immediately, regardless of the
	// heartbeat clock.
	cache.markStreamDisconnected()
	if cache.Ready() {
		t.Fatal("cache stayed ready after the event stream disconnected")
	}

	// Reconnecting resets the heartbeat clock too, so a stale reconnect does not
	// immediately look stale again.
	now = now.Add(5 * time.Minute)
	cache.markStreamConnected()
	if !cache.Ready() {
		t.Fatal("cache did not become ready again after reconnecting")
	}
}

var errFakeResourceCacheServer = fakeErr("simulated Incus API failure")

type fakeErr string

func (e fakeErr) Error() string { return string(e) }
