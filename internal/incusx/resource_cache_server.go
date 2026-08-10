package incusx

import (
	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	authapp "github.com/thieso2/sandcastle-incus/internal/authapp"
)

// ResourceCacheServer implements authapp.ResourceCacheServer against a live
// Incus daemon — the Auth App's mounted host socket, the same server used
// everywhere else in this package. It exists purely to give the cache engine
// in internal/authapp the all-projects/per-project read surface it needs
// without that package importing the Incus client SDK directly, mirroring
// RouteBackend/CaddyController (see routebackend.go).
type ResourceCacheServer struct {
	inner incus.InstanceServer
	Log   func(string)
}

var _ authapp.ResourceCacheServer = ResourceCacheServer{}

// NewResourceCacheServer wraps server for use as the cache engine's Incus
// source. WithVerbose enables the same "[verbose] incus api: …" trace style
// used by the rest of incusx.
func NewResourceCacheServer(server incus.InstanceServer) ResourceCacheServer {
	return ResourceCacheServer{inner: server}
}

// WithVerbose enables per-call "[verbose] incus api: …" tracing to w, matching
// the rest of incusx's Incus API call sites.
func (s ResourceCacheServer) WithVerbose(verbose bool, w interface{ Write([]byte) (int, error) }) ResourceCacheServer {
	if !verbose {
		s.Log = nil
		return s
	}
	s.Log = func(msg string) { _, _ = w.Write([]byte(msg)) }
	return s
}

func (s ResourceCacheServer) GetInstancesFull(instanceType api.InstanceType) ([]api.InstanceFull, error) {
	return logIncusAPICall(s.Log, "GetInstancesFull type="+string(instanceType), func() ([]api.InstanceFull, error) {
		return s.inner.GetInstancesFull(instanceType)
	})
}

func (s ResourceCacheServer) GetNetworks() ([]api.Network, error) {
	return logIncusAPICall(s.Log, "GetNetworks", func() ([]api.Network, error) {
		return s.inner.GetNetworks()
	})
}

func (s ResourceCacheServer) GetStoragePoolVolumesFull(pool string) ([]api.StorageVolumeFull, error) {
	return logIncusAPICall(s.Log, "GetStoragePoolVolumesFull pool="+pool, func() ([]api.StorageVolumeFull, error) {
		return s.inner.GetStoragePoolVolumesFull(pool)
	})
}

func (s ResourceCacheServer) GetProfiles() ([]api.Profile, error) {
	return logIncusAPICall(s.Log, "GetProfiles", func() ([]api.Profile, error) {
		return s.inner.GetProfiles()
	})
}

func (s ResourceCacheServer) GetImages() ([]api.Image, error) {
	return logIncusAPICall(s.Log, "GetImages", func() ([]api.Image, error) {
		return s.inner.GetImages()
	})
}

func (s ResourceCacheServer) UseProject(name string) authapp.ResourceCacheProjectServer {
	return ResourceCacheServer{inner: s.inner.UseProject(name), Log: s.Log}
}

func (s ResourceCacheServer) GetInstancesFullAllProjects(instanceType api.InstanceType) ([]api.InstanceFull, error) {
	return logIncusAPICall(s.Log, "GetInstancesFullAllProjects type="+string(instanceType), func() ([]api.InstanceFull, error) {
		return s.inner.GetInstancesFullAllProjects(instanceType)
	})
}

func (s ResourceCacheServer) GetNetworksAllProjects() ([]api.Network, error) {
	return logIncusAPICall(s.Log, "GetNetworksAllProjects", func() ([]api.Network, error) {
		return s.inner.GetNetworksAllProjects()
	})
}

func (s ResourceCacheServer) GetStoragePools() ([]api.StoragePool, error) {
	return logIncusAPICall(s.Log, "GetStoragePools", func() ([]api.StoragePool, error) {
		return s.inner.GetStoragePools()
	})
}

func (s ResourceCacheServer) GetStoragePoolVolumesFullAllProjects(pool string) ([]api.StorageVolumeFull, error) {
	return logIncusAPICall(s.Log, "GetStoragePoolVolumesFullAllProjects pool="+pool, func() ([]api.StorageVolumeFull, error) {
		return s.inner.GetStoragePoolVolumesFullAllProjects(pool)
	})
}

func (s ResourceCacheServer) GetProfilesAllProjects() ([]api.Profile, error) {
	return logIncusAPICall(s.Log, "GetProfilesAllProjects", func() ([]api.Profile, error) {
		return s.inner.GetProfilesAllProjects()
	})
}

func (s ResourceCacheServer) GetImagesAllProjects() ([]api.Image, error) {
	return logIncusAPICall(s.Log, "GetImagesAllProjects", func() ([]api.Image, error) {
		return s.inner.GetImagesAllProjects()
	})
}

func (s ResourceCacheServer) GetEventsAllProjects() (*incus.EventListener, error) {
	return s.inner.GetEventsAllProjects()
}
