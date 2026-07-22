package incusx

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type HostOverrideServer interface {
	UseProject(name string) HostOverrideResourceServer
}

type HostOverrideResourceServer interface {
	GetInstances(instanceType api.InstanceType) ([]api.Instance, error)
	GetInstancesFull(instanceType api.InstanceType) ([]api.InstanceFull, error)
	GetInstance(name string) (*api.Instance, string, error)
	UpdateInstance(name string, instance api.InstancePut, ETag string) (incus.Operation, error)
	CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error
	GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error)
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}

type HostOverrideManager struct {
	Remote     string
	ConfigPath string
	Server     HostOverrideServer
	Log        func(string)
}

func NewHostOverrideManager(remote string) HostOverrideManager {
	return HostOverrideManager{Remote: remote}
}

func NewHostOverrideManagerForServer(server incus.InstanceServer) HostOverrideManager {
	return HostOverrideManager{Server: sdkHostOverrideServer{inner: server}}
}

func NewHostOverrideManagerForSharedRemote(remote *SharedRemote) HostOverrideManager {
	return HostOverrideManager{Server: sharedHostOverrideServer{remote: remote}, Log: remote.Log}
}

func (m HostOverrideManager) WithVerbose(enabled bool, w io.Writer) HostOverrideManager {
	if enabled {
		m.Log = func(msg string) { fmt.Fprint(w, msg) }
	}
	return m
}

func (m HostOverrideManager) FindMachine(ctx context.Context, summary tenant.Summary, projectName string, machineName string) (meta.Machine, error) {
	machines, err := m.ListMachines(ctx, summary)
	if err != nil {
		return meta.Machine{}, err
	}
	for _, machine := range machines {
		if machine.Project == projectName && machine.Name == machineName {
			return machine, nil
		}
	}
	return meta.Machine{}, fmt.Errorf("Sandcastle machine %s/%s/%s not found", summary.Tenant, projectName, machineName)
}

func (m HostOverrideManager) ListMachines(ctx context.Context, summary tenant.Summary) ([]meta.Machine, error) {
	return m.listV2Machines(summary)
}

// ListMachinesAndUnmanaged: v2 machines are freeform instances, so every
// instance is a first-class machine and the "unmanaged" bucket is always empty.
func (m HostOverrideManager) ListMachinesAndUnmanaged(ctx context.Context, summary tenant.Summary) ([]meta.Machine, []machine.UnmanagedMachine, error) {
	machines, err := m.listV2Machines(summary)
	return machines, nil, err
}

// listV2Machines lists every instance across a v2 tenant's app projects. In v2
// machines are freeform incus instances (CTs and VMs, no Sandcastle metadata),
// so all of them are first-class machines — there is no unmanaged bucket.
func (m HostOverrideManager) listV2Machines(summary tenant.Summary) ([]meta.Machine, error) {
	server, err := m.resolveServer()
	if err != nil {
		return nil, err
	}
	projects := summary.Projects
	if len(projects) == 0 {
		projects = []meta.Project{{Name: naming.DefaultProjectName}}
	}
	machines := []meta.Machine{}
	for _, project := range projects {
		projectServer := server.UseProject(summary.V2IncusProjectName(project.Name))
		instances, err := projectServer.GetInstancesFull(api.InstanceTypeAny)
		if err != nil {
			return nil, fmt.Errorf("list project %s instances: %w", project.Name, err)
		}
		for _, instance := range instances {
			if meta.IsManaged(instance.Config) && instance.Config[meta.KeyKind] == meta.KindSidecar {
				continue
			}
			machines = append(machines, meta.Machine{
				Tenant:    summary.Tenant,
				Project:   project.Name,
				Name:      instance.Name,
				Type:      string(instance.Type),
				PrivateIP: instanceGlobalIPv4(instance),
				CreatedAt: formatInstanceCreatedAt(instance.CreatedAt),
				Running:   instance.IsActive(),
			})
		}
	}
	return machines, nil
}

// instanceGlobalIPv4 returns the global IPv4 of the instance's Incus-managed
// NIC from live state — freeform v2 machines lease via DHCP, so there is no
// static ip device to read. In-guest bridges such as docker0 are ignored; see
// instance_ipv4.go.
func instanceGlobalIPv4(instance api.InstanceFull) string {
	if instance.State == nil {
		return ""
	}
	return instanceNICIPv4(instance.ExpandedConfig, instance.ExpandedDevices, instance.State.Network).Address
}

// resolveServer returns the connected server the manager should use (the
// injected one for tests, otherwise a fresh SDK connection to the remote).
func (m HostOverrideManager) resolveServer() (HostOverrideServer, error) {
	server := m.Server
	if server == nil {
		instanceServer, err := connectConfiguredRemote(m.Log, m.ConfigPath, m.Remote)
		if err != nil {
			return nil, err
		}
		server = sdkHostOverrideServer{inner: instanceServer, Log: m.Log}
	}
	if connector, ok := server.(interface{ ensureConnected() error }); ok {
		if err := connector.ensureConnected(); err != nil {
			return nil, err
		}
	}
	return server, nil
}

func formatInstanceCreatedAt(createdAt time.Time) string {
	if createdAt.IsZero() {
		return ""
	}
	return createdAt.UTC().Format(time.RFC3339)
}

func instancePrivateIP(instance api.Instance) string {
	for _, devices := range []map[string]map[string]string{instance.ExpandedDevices, instance.Devices} {
		for _, device := range devices {
			if device["type"] != "nic" {
				continue
			}
			if ip := device["ipv4.address"]; ip != "" && ip != "none" && ip != "auto" {
				return ip
			}
		}
	}
	return ""
}

type sdkHostOverrideServer struct {
	inner incus.InstanceServer
	Log   func(string)
}

func (s sdkHostOverrideServer) UseProject(name string) HostOverrideResourceServer {
	return sdkHostOverrideResourceServer{inner: s.inner.UseProject(name), projectName: name, Log: s.Log}
}

type sdkHostOverrideResourceServer struct {
	inner       incus.InstanceServer
	projectName string
	Log         func(string)
}

func (s sdkHostOverrideResourceServer) GetInstances(instanceType api.InstanceType) ([]api.Instance, error) {
	return logIncusAPICall(s.Log, "GetInstances project="+s.projectName+" type="+string(instanceType), func() ([]api.Instance, error) {
		return s.inner.GetInstances(instanceType)
	})
}

func (s sdkHostOverrideResourceServer) GetInstancesFull(instanceType api.InstanceType) ([]api.InstanceFull, error) {
	return logIncusAPICall(s.Log, "GetInstancesFull project="+s.projectName+" type="+string(instanceType), func() ([]api.InstanceFull, error) {
		return s.inner.GetInstancesFull(instanceType)
	})
}

func (s sdkHostOverrideResourceServer) GetInstance(name string) (*api.Instance, string, error) {
	var instance *api.Instance
	var etag string
	err := logIncusAPICall0(s.Log, "GetInstance project="+s.projectName+" instance="+name, func() error {
		var err error
		instance, etag, err = s.inner.GetInstance(name)
		return err
	})
	return instance, etag, err
}

func (s sdkHostOverrideResourceServer) UpdateInstance(name string, instance api.InstancePut, ETag string) (incus.Operation, error) {
	return logIncusAPICall(s.Log, "UpdateInstance project="+s.projectName+" instance="+name, func() (incus.Operation, error) {
		return s.inner.UpdateInstance(name, instance, ETag)
	})
}

func (s sdkHostOverrideResourceServer) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
	return logIncusAPICall0(s.Log, "CreateInstanceFile project="+s.projectName+" instance="+instanceName+" path="+path, func() error {
		return s.inner.CreateInstanceFile(instanceName, path, args)
	})
}

func (s sdkHostOverrideResourceServer) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	var content io.ReadCloser
	var response *incus.InstanceFileResponse
	err := logIncusAPICall0(s.Log, "GetStorageVolumeFile project="+s.projectName+" pool="+pool+" volume="+volumeName+" path="+filePath, func() error {
		var err error
		content, response, err = getStorageVolumeFile(s.inner, s.projectName, pool, volumeType, volumeName, filePath)
		return err
	})
	return content, response, err
}

func (s sdkHostOverrideResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	return logIncusAPICall(s.Log, "ExecInstance project="+s.projectName+" instance="+instanceName, func() (incus.Operation, error) {
		return s.inner.ExecInstance(instanceName, exec, args)
	})
}

func appendUnique(values []string, additions ...string) []string {
	seen := map[string]bool{}
	output := []string{}
	for _, value := range append(values, additions...) {
		normalized := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		output = append(output, normalized)
	}
	return output
}

func removeValue(values []string, removed string) []string {
	normalizedRemoved := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(removed)), ".")
	output := []string{}
	for _, value := range values {
		normalized := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
		if normalized == "" || normalized == normalizedRemoved {
			continue
		}
		output = append(output, normalized)
	}
	return output
}
