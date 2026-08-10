package authapp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

// ResourceCacheProjectServer is the per-project subset of an Incus server the
// cache uses to refresh a single project's resources after a lifecycle event.
// incus.InstanceServer.UseProject(...) already returns exactly this shape —
// see incusx.ResourceCacheServer for the adapter.
type ResourceCacheProjectServer interface {
	GetInstancesFull(instanceType api.InstanceType) ([]api.InstanceFull, error)
	GetNetworks() ([]api.Network, error)
	GetStoragePoolVolumesFull(pool string) ([]api.StorageVolumeFull, error)
	GetProfiles() ([]api.Profile, error)
	GetImages() ([]api.Image, error)
}

// ResourceCacheServer is the Incus surface the cache engine needs: one
// all-projects read per resource type to seed the cache, the per-project
// re-reads (via UseProject) to refresh a single project after a lifecycle
// event, and the event bus subscription that drives those refreshes.
type ResourceCacheServer interface {
	UseProject(name string) ResourceCacheProjectServer
	GetInstancesFullAllProjects(instanceType api.InstanceType) ([]api.InstanceFull, error)
	GetNetworksAllProjects() ([]api.Network, error)
	GetStoragePools() ([]api.StoragePool, error)
	GetStoragePoolVolumesFullAllProjects(pool string) ([]api.StorageVolumeFull, error)
	GetProfilesAllProjects() ([]api.Profile, error)
	GetImagesAllProjects() ([]api.Image, error)
	GetEventsAllProjects() (*incus.EventListener, error)
}

// resourceCacheKind names which per-project bucket a lifecycle action affects.
type resourceCacheKind int

const (
	resourceCacheKindInstance resourceCacheKind = iota
	resourceCacheKindNetwork
	resourceCacheKindStorageVolume
	resourceCacheKindStoragePool
	resourceCacheKindProfile
	resourceCacheKindImage
)

// resourceCacheActions maps each lifecycle action this cache cares about to
// the resource kind it affects. Unlike admin_dns_events.go's DNS reconciler —
// which deliberately excludes noisy actions such as instance-file-retrieved
// and instance-exec because a periodic ticker backstops anything it misses —
// this cache has no such backstop (no periodic resync; see
// implementation-notes.md), so it must include every action that can change a
// field `sc ls` shows, most notably instance-updated (IP/NIC changes). It
// still excludes actions that cannot change any listed field regardless of
// resync strategy: console/exec/file/log/metadata-retrieval and
// snapshot/backup events, none of which alter the parent resource's own
// name, project, state, or address.
var resourceCacheActions = map[string]resourceCacheKind{
	api.EventLifecycleInstanceCreated:   resourceCacheKindInstance,
	api.EventLifecycleInstanceDeleted:   resourceCacheKindInstance,
	api.EventLifecycleInstanceRenamed:   resourceCacheKindInstance,
	api.EventLifecycleInstanceUpdated:   resourceCacheKindInstance,
	api.EventLifecycleInstanceMigrated:  resourceCacheKindInstance,
	api.EventLifecycleInstanceRestarted: resourceCacheKindInstance,
	api.EventLifecycleInstanceRestored:  resourceCacheKindInstance,
	api.EventLifecycleInstanceResumed:   resourceCacheKindInstance,
	api.EventLifecycleInstanceShutdown:  resourceCacheKindInstance,
	api.EventLifecycleInstanceStarted:   resourceCacheKindInstance,
	api.EventLifecycleInstanceStopped:   resourceCacheKindInstance,

	api.EventLifecycleNetworkCreated: resourceCacheKindNetwork,
	api.EventLifecycleNetworkDeleted: resourceCacheKindNetwork,
	api.EventLifecycleNetworkRenamed: resourceCacheKindNetwork,
	api.EventLifecycleNetworkUpdated: resourceCacheKindNetwork,

	api.EventLifecycleStorageVolumeCreated:  resourceCacheKindStorageVolume,
	api.EventLifecycleStorageVolumeDeleted:  resourceCacheKindStorageVolume,
	api.EventLifecycleStorageVolumeRenamed:  resourceCacheKindStorageVolume,
	api.EventLifecycleStorageVolumeUpdated:  resourceCacheKindStorageVolume,
	api.EventLifecycleStorageVolumeRestored: resourceCacheKindStorageVolume,

	api.EventLifecycleStoragePoolCreated: resourceCacheKindStoragePool,
	api.EventLifecycleStoragePoolDeleted: resourceCacheKindStoragePool,
	api.EventLifecycleStoragePoolUpdated: resourceCacheKindStoragePool,

	api.EventLifecycleProfileCreated: resourceCacheKindProfile,
	api.EventLifecycleProfileDeleted: resourceCacheKindProfile,
	api.EventLifecycleProfileRenamed: resourceCacheKindProfile,
	api.EventLifecycleProfileUpdated: resourceCacheKindProfile,

	api.EventLifecycleImageCreated:      resourceCacheKindImage,
	api.EventLifecycleImageDeleted:      resourceCacheKindImage,
	api.EventLifecycleImageUpdated:      resourceCacheKindImage,
	api.EventLifecycleImageRefreshed:    resourceCacheKindImage,
	api.EventLifecycleImageAliasCreated: resourceCacheKindImage,
	api.EventLifecycleImageAliasDeleted: resourceCacheKindImage,
	api.EventLifecycleImageAliasRenamed: resourceCacheKindImage,
	api.EventLifecycleImageAliasUpdated: resourceCacheKindImage,
}

// ResourceCacheLogf is a minimal logging seam so RunResourceCache can report
// seed/reconnect failures without pulling svclog into this package; pass nil
// for silent operation (e.g. in tests).
type ResourceCacheLogf func(format string, args ...any)

func (log ResourceCacheLogf) call(format string, args ...any) {
	if log != nil {
		log(format, args...)
	}
}

// seedResourceCache performs the one full, all-projects read across all five
// resource types that populates a freshly started cache.
func seedResourceCache(cache *ResourceCache, server ResourceCacheServer) error {
	instances, err := server.GetInstancesFullAllProjects(api.InstanceTypeAny)
	if err != nil {
		return fmt.Errorf("list instances across projects: %w", err)
	}
	networks, err := server.GetNetworksAllProjects()
	if err != nil {
		return fmt.Errorf("list networks across projects: %w", err)
	}
	pools, err := server.GetStoragePools()
	if err != nil {
		return fmt.Errorf("list storage pools: %w", err)
	}
	var volumes []api.StorageVolumeFull
	for _, pool := range pools {
		poolVolumes, err := server.GetStoragePoolVolumesFullAllProjects(pool.Name)
		if err != nil {
			return fmt.Errorf("list storage volumes for pool %s across projects: %w", pool.Name, err)
		}
		volumes = append(volumes, poolVolumes...)
	}
	profiles, err := server.GetProfilesAllProjects()
	if err != nil {
		return fmt.Errorf("list profiles across projects: %w", err)
	}
	images, err := server.GetImagesAllProjects()
	if err != nil {
		return fmt.Errorf("list images across projects: %w", err)
	}
	cache.seed(instances, networks, pools, volumes, profiles, images)
	return nil
}

// refreshResourceCacheProject re-reads one project's resources for the kind a
// lifecycle event named, and upserts them into the cache. Keying purely off
// (kind, project) — never off the event's resource name — sidesteps having to
// interpret rename event payloads (whether Name is the old or new name is not
// documented) at the cost of one slightly wider re-read per event.
func refreshResourceCacheProject(cache *ResourceCache, server ResourceCacheServer, kind resourceCacheKind, project string) error {
	if kind == resourceCacheKindStoragePool {
		pools, err := server.GetStoragePools()
		if err != nil {
			return fmt.Errorf("refresh storage pools: %w", err)
		}
		cache.setStoragePools(pools)
		return nil
	}
	projectServer := server.UseProject(project)
	switch kind {
	case resourceCacheKindInstance:
		list, err := projectServer.GetInstancesFull(api.InstanceTypeAny)
		if err != nil {
			return fmt.Errorf("refresh instances for project %s: %w", project, err)
		}
		cache.setProjectInstances(project, list)
	case resourceCacheKindNetwork:
		list, err := projectServer.GetNetworks()
		if err != nil {
			return fmt.Errorf("refresh networks for project %s: %w", project, err)
		}
		cache.setProjectNetworks(project, list)
	case resourceCacheKindStorageVolume:
		var volumes []api.StorageVolumeFull
		for _, pool := range cache.storagePoolNames() {
			poolVolumes, err := projectServer.GetStoragePoolVolumesFull(pool)
			if err != nil {
				return fmt.Errorf("refresh storage volumes for project %s pool %s: %w", project, pool, err)
			}
			volumes = append(volumes, poolVolumes...)
		}
		cache.setProjectStorageVolumes(project, volumes)
	case resourceCacheKindProfile:
		list, err := projectServer.GetProfiles()
		if err != nil {
			return fmt.Errorf("refresh profiles for project %s: %w", project, err)
		}
		cache.setProjectProfiles(project, list)
	case resourceCacheKindImage:
		list, err := projectServer.GetImages()
		if err != nil {
			return fmt.Errorf("refresh images for project %s: %w", project, err)
		}
		cache.setProjectImages(project, list)
	}
	return nil
}

// handleResourceCacheEvent dispatches one lifecycle event to the refresh it
// implies, if any. Events for actions this cache does not track are ignored.
func handleResourceCacheEvent(cache *ResourceCache, server ResourceCacheServer, event api.Event, log ResourceCacheLogf) {
	var lifecycle api.EventLifecycle
	if json.Unmarshal(event.Metadata, &lifecycle) != nil {
		return
	}
	kind, ok := resourceCacheActions[lifecycle.Action]
	if !ok {
		return
	}
	if err := refreshResourceCacheProject(cache, server, kind, event.Project); err != nil {
		log.call("[resource-cache] %v", err)
	}
}

// RunResourceCache seeds the cache with one full read, then subscribes to the
// Incus event bus and keeps the cache current until ctx is done, reconnecting
// with backoff whenever the seed fails or the event socket drops — mirroring
// admin_dns_events.go's subscribeInstanceLifecycleEvents. It blocks; call it
// in a goroutine.
func RunResourceCache(ctx context.Context, cache *ResourceCache, server ResourceCacheServer, log ResourceCacheLogf) {
	for ctx.Err() == nil {
		if err := seedResourceCache(cache, server); err != nil {
			log.call("[resource-cache] initial read failed: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		runResourceCacheEventLoop(ctx, cache, server, log)
	}
}

// runResourceCacheEventLoop owns exactly one event-bus connection lifecycle:
// connect, mark ready, dispatch until the socket drops or ctx is done, mark
// not-ready. Returning (rather than looping internally) lets RunResourceCache
// re-seed after a drop, since events missed while disconnected can only be
// recovered by a fresh full read.
func runResourceCacheEventLoop(ctx context.Context, cache *ResourceCache, server ResourceCacheServer, log ResourceCacheLogf) {
	listener, err := server.GetEventsAllProjects()
	if err != nil {
		log.call("[resource-cache] event subscription failed: %v", err)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
		return
	}
	// A handler with nil types matches every event, lifecycle or not — the
	// heartbeat only needs proof the socket is alive, not that anything
	// cache-relevant happened (see recordHeartbeat and
	// implementation-notes.md for why: the Incus event bus has no dedicated
	// liveness ping).
	_, _ = listener.AddHandler(nil, func(api.Event) { cache.recordHeartbeat() })
	_, err = listener.AddHandler([]string{api.EventTypeLifecycle}, func(event api.Event) {
		handleResourceCacheEvent(cache, server, event, log)
	})
	if err != nil {
		listener.Disconnect()
		log.call("[resource-cache] event handler registration failed: %v", err)
		return
	}
	cache.markStreamConnected()
	defer cache.markStreamDisconnected()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			listener.Disconnect()
		case <-done:
		}
	}()
	_ = listener.Wait()
	close(done)
}
