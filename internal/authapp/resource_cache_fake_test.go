package authapp

import (
	"encoding/json"
	"errors"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

// fakeProjectData is one project's live Incus state, as a test would set it up
// before seeding the cache or before firing a synthetic event that should
// cause a refresh.
type fakeProjectData struct {
	instances     []api.InstanceFull
	networks      []api.Network
	volumesByPool map[string][]api.StorageVolumeFull
	profiles      []api.Profile
	images        []api.Image
}

// fakeResourceCacheServer is an in-memory stand-in for authapp.ResourceCacheServer
// (an Incus daemon). Tests mutate its project data directly to simulate a live
// change, then either seed the cache from it or hand handleResourceCacheEvent a
// synthetic event and assert the cache picked the change up.
type fakeResourceCacheServer struct {
	projects map[string]*fakeProjectData
	pools    []api.StoragePool
	err      error
	// volumeErr fails ONLY the storage-volume reads, the way a real host does
	// when one instance's backing dataset is gone: every other resource type
	// is a plain database read and keeps working.
	volumeErr error
}

func newFakeResourceCacheServer() *fakeResourceCacheServer {
	return &fakeResourceCacheServer{projects: map[string]*fakeProjectData{}}
}

func (f *fakeResourceCacheServer) project(name string) *fakeProjectData {
	p, ok := f.projects[name]
	if !ok {
		p = &fakeProjectData{volumesByPool: map[string][]api.StorageVolumeFull{}}
		f.projects[name] = p
	}
	return p
}

func (f *fakeResourceCacheServer) GetInstancesFullAllProjects(api.InstanceType) ([]api.InstanceFull, error) {
	if f.err != nil {
		return nil, f.err
	}
	var all []api.InstanceFull
	for _, p := range f.projects {
		all = append(all, p.instances...)
	}
	return all, nil
}

func (f *fakeResourceCacheServer) GetNetworksAllProjects() ([]api.Network, error) {
	if f.err != nil {
		return nil, f.err
	}
	var all []api.Network
	for _, p := range f.projects {
		all = append(all, p.networks...)
	}
	return all, nil
}

func (f *fakeResourceCacheServer) GetStoragePools() ([]api.StoragePool, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.pools, nil
}

func (f *fakeResourceCacheServer) GetStoragePoolVolumesFullAllProjects(pool string) ([]api.StorageVolumeFull, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.volumeErr != nil {
		return nil, f.volumeErr
	}
	var all []api.StorageVolumeFull
	for _, p := range f.projects {
		all = append(all, p.volumesByPool[pool]...)
	}
	return all, nil
}

func (f *fakeResourceCacheServer) GetProfilesAllProjects() ([]api.Profile, error) {
	if f.err != nil {
		return nil, f.err
	}
	var all []api.Profile
	for _, p := range f.projects {
		all = append(all, p.profiles...)
	}
	return all, nil
}

func (f *fakeResourceCacheServer) GetImagesAllProjects() ([]api.Image, error) {
	if f.err != nil {
		return nil, f.err
	}
	var all []api.Image
	for _, p := range f.projects {
		all = append(all, p.images...)
	}
	return all, nil
}

func (f *fakeResourceCacheServer) GetEventsAllProjects() (*incus.EventListener, error) {
	return nil, errors.New("fakeResourceCacheServer does not support live event listeners")
}

func (f *fakeResourceCacheServer) UseProject(name string) ResourceCacheProjectServer {
	return fakeProjectServer{data: f.project(name), err: &f.err, volumeErr: &f.volumeErr}
}

// fakeProjectServer is the per-project view UseProject returns: reads reflect
// whatever the test has put into data right now.
type fakeProjectServer struct {
	data      *fakeProjectData
	err       *error
	volumeErr *error
}

func (s fakeProjectServer) GetInstancesFull(api.InstanceType) ([]api.InstanceFull, error) {
	if *s.err != nil {
		return nil, *s.err
	}
	return s.data.instances, nil
}

func (s fakeProjectServer) GetNetworks() ([]api.Network, error) {
	if *s.err != nil {
		return nil, *s.err
	}
	return s.data.networks, nil
}

func (s fakeProjectServer) GetStoragePoolVolumesFull(pool string) ([]api.StorageVolumeFull, error) {
	if *s.err != nil {
		return nil, *s.err
	}
	if s.volumeErr != nil && *s.volumeErr != nil {
		return nil, *s.volumeErr
	}
	return s.data.volumesByPool[pool], nil
}

func (s fakeProjectServer) GetProfiles() ([]api.Profile, error) {
	if *s.err != nil {
		return nil, *s.err
	}
	return s.data.profiles, nil
}

func (s fakeProjectServer) GetImages() ([]api.Image, error) {
	if *s.err != nil {
		return nil, *s.err
	}
	return s.data.images, nil
}

// lifecycleEvent builds a synthetic api.Event carrying an api.EventLifecycle
// for action/project, as handleResourceCacheEvent expects to unmarshal.
func lifecycleEvent(action, project string) api.Event {
	metadata, err := json.Marshal(api.EventLifecycle{Action: action, Project: project})
	if err != nil {
		panic(err)
	}
	return api.Event{
		Type:     api.EventTypeLifecycle,
		Project:  project,
		Metadata: metadata,
	}
}
