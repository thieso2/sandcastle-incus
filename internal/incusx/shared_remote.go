package incusx

import (
	"fmt"
	"io"
	"sync"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
)

type SharedRemote struct {
	Remote     string
	ConfigPath string
	Log        func(string)

	mu     sync.Mutex
	server incus.InstanceServer
}

func NewSharedRemote(remote string) *SharedRemote {
	return &SharedRemote{Remote: remote}
}

func (r *SharedRemote) WithVerbose(enabled bool, w io.Writer) *SharedRemote {
	if enabled {
		r.Log = func(msg string) { fmt.Fprint(w, msg) }
	}
	return r
}

func (r *SharedRemote) instanceServer() (incus.InstanceServer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.server != nil {
		return r.server, nil
	}
	loaded, err := cliconfig.LoadConfig(r.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load Incus config: %w", err)
	}
	remote := r.Remote
	if remote == "" {
		remote = loaded.DefaultRemote
	}
	server, err := logIncusAPICall(r.Log, "connect remote "+remote, func() (incus.InstanceServer, error) {
		return loaded.GetInstanceServer(remote)
	})
	if err != nil {
		return nil, fmt.Errorf("connect to Incus remote %q: %w", remote, err)
	}
	r.server = server
	return server, nil
}

type sharedTenantListServer struct {
	remote *SharedRemote
}

func (s sharedTenantListServer) ensureConnected() error {
	_, err := s.remote.instanceServer()
	return err
}

func (s sharedTenantListServer) GetProjects() ([]api.Project, error) {
	server, err := s.remote.instanceServer()
	if err != nil {
		return nil, err
	}
	return server.GetProjects()
}

type sharedTenantMetadataServer struct {
	remote *SharedRemote
}

func (s sharedTenantMetadataServer) UseProject(name string) TenantMetadataResourceServer {
	return sharedTenantMetadataResourceServer{remote: s.remote, projectName: name}
}

type sharedTenantMetadataResourceServer struct {
	remote      *SharedRemote
	projectName string
}

func (s sharedTenantMetadataResourceServer) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	server, err := s.remote.instanceServer()
	if err != nil {
		return nil, nil, err
	}
	return sdkTenantMetadataResourceServer{inner: server.UseProject(s.projectName), projectName: s.projectName, Log: s.remote.Log}.GetStorageVolumeFile(pool, volumeType, volumeName, filePath)
}

type sharedHostOverrideServer struct {
	remote *SharedRemote
}

func (s sharedHostOverrideServer) ensureConnected() error {
	_, err := s.remote.instanceServer()
	return err
}

func (s sharedHostOverrideServer) UseProject(name string) HostOverrideResourceServer {
	return sharedHostOverrideResourceServer{remote: s.remote, projectName: name}
}

type sharedHostOverrideResourceServer struct {
	remote      *SharedRemote
	projectName string
}

func (s sharedHostOverrideResourceServer) resource() (sdkHostOverrideResourceServer, error) {
	server, err := s.remote.instanceServer()
	if err != nil {
		return sdkHostOverrideResourceServer{}, err
	}
	return sdkHostOverrideResourceServer{inner: server.UseProject(s.projectName), projectName: s.projectName, Log: s.remote.Log}, nil
}

func (s sharedHostOverrideResourceServer) GetInstances(instanceType api.InstanceType) ([]api.Instance, error) {
	resource, err := s.resource()
	if err != nil {
		return nil, err
	}
	return resource.GetInstances(instanceType)
}

func (s sharedHostOverrideResourceServer) GetInstance(name string) (*api.Instance, string, error) {
	resource, err := s.resource()
	if err != nil {
		return nil, "", err
	}
	return resource.GetInstance(name)
}

func (s sharedHostOverrideResourceServer) UpdateInstance(name string, instance api.InstancePut, ETag string) (incus.Operation, error) {
	resource, err := s.resource()
	if err != nil {
		return nil, err
	}
	return resource.UpdateInstance(name, instance, ETag)
}

func (s sharedHostOverrideResourceServer) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
	resource, err := s.resource()
	if err != nil {
		return err
	}
	return resource.CreateInstanceFile(instanceName, path, args)
}

func (s sharedHostOverrideResourceServer) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	resource, err := s.resource()
	if err != nil {
		return nil, nil, err
	}
	return resource.GetStorageVolumeFile(pool, volumeType, volumeName, filePath)
}

func (s sharedHostOverrideResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	resource, err := s.resource()
	if err != nil {
		return nil, err
	}
	return resource.ExecInstance(instanceName, exec, args)
}
