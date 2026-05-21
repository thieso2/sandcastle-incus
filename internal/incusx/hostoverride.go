package incusx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/caddy"
	"github.com/thieso2/sandcastle-incus/internal/hostoverride"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type HostOverrideServer interface {
	UseProject(name string) HostOverrideResourceServer
}

type HostOverrideResourceServer interface {
	GetInstances(instanceType api.InstanceType) ([]api.Instance, error)
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
}

func NewHostOverrideManager(remote string) HostOverrideManager {
	return HostOverrideManager{Remote: remote}
}

func NewHostOverrideManagerForServer(server incus.InstanceServer) HostOverrideManager {
	return HostOverrideManager{Server: sdkHostOverrideServer{inner: server}}
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
	instances, err := m.listTenantInstances(summary)
	if err != nil {
		return nil, err
	}
	machines := []meta.Machine{}
	for _, instance := range instances {
		if instance.Config[meta.KeyKind] != meta.KindMachine {
			continue
		}
		machine, err := meta.ParseMachineConfig(map[string]string(instance.Config))
		if err != nil {
			return nil, fmt.Errorf("parse machine metadata for %s: %w", instance.Name, err)
		}
		machine.CreatedAt = formatInstanceCreatedAt(instance.CreatedAt)
		machine.Running = instance.IsActive()
		machines = append(machines, machine)
	}
	return machines, nil
}

func (m HostOverrideManager) ListUnmanagedMachines(ctx context.Context, summary tenant.Summary) ([]machine.UnmanagedMachine, error) {
	instances, err := m.listTenantInstances(summary)
	if err != nil {
		return nil, err
	}
	unmanaged := []machine.UnmanagedMachine{}
	for _, instance := range instances {
		if instance.Config[meta.KeyKind] == meta.KindMachine {
			continue
		}
		unmanaged = append(unmanaged, machine.UnmanagedMachine{
			Tenant:       summary.Tenant,
			Name:         instance.Name,
			InstanceName: instance.Name,
			Type:         string(instance.Type),
			PrivateIP:    instancePrivateIP(instance),
			Status:       instance.Status,
			CreatedAt:    formatInstanceCreatedAt(instance.CreatedAt),
			Running:      instance.IsActive(),
		})
	}
	return unmanaged, nil
}

func (m HostOverrideManager) listTenantInstances(summary tenant.Summary) ([]api.Instance, error) {
	server := m.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(m.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("load Incus config: %w", err)
		}
		remote := m.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return nil, fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkHostOverrideServer{inner: instanceServer}
	}
	projectServer := server.UseProject(summary.IncusName)
	instances, err := projectServer.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		return nil, fmt.Errorf("list project instances: %w", err)
	}
	return instances, nil
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

func (m HostOverrideManager) Add(ctx context.Context, plan hostoverride.AddPlan) error {
	server := m.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(m.ConfigPath)
		if err != nil {
			return fmt.Errorf("load Incus config: %w", err)
		}
		remote := m.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkHostOverrideServer{inner: instanceServer}
	}
	projectServer := server.UseProject(plan.Tenant.IncusName)
	updatedMachine, err := updateMachineExtraSANs(projectServer, plan)
	if err != nil {
		return err
	}
	return writeHostOverrideMachineFiles(projectServer, plan, updatedMachine)
}

func (m HostOverrideManager) Delete(ctx context.Context, plan hostoverride.DeletePlan) error {
	server := m.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(m.ConfigPath)
		if err != nil {
			return fmt.Errorf("load Incus config: %w", err)
		}
		remote := m.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkHostOverrideServer{inner: instanceServer}
	}
	projectServer := server.UseProject(plan.Tenant.IncusName)
	updatedMachine, err := removeMachineExtraSAN(projectServer, plan)
	if err != nil {
		return err
	}
	return writeHostOverrideMachineFiles(projectServer, addPlanFromDelete(plan), updatedMachine)
}

type sdkHostOverrideServer struct {
	inner incus.InstanceServer
}

func removeMachineExtraSAN(server HostOverrideResourceServer, plan hostoverride.DeletePlan) (meta.Machine, error) {
	instance, etag, err := server.GetInstance(plan.InstanceName)
	if err != nil {
		return meta.Machine{}, fmt.Errorf("get machine %s: %w", plan.InstanceName, err)
	}
	put := instance.Writable()
	config := map[string]string(put.Config)
	state, err := meta.ParseMachineConfig(config)
	if err != nil {
		return meta.Machine{}, fmt.Errorf("parse machine metadata for %s: %w", plan.InstanceName, err)
	}
	state.ExtraSANs = removeValue(state.ExtraSANs, plan.Hostname)
	updated, err := meta.MachineConfig(state)
	if err != nil {
		return meta.Machine{}, err
	}
	for key, value := range updated {
		config[key] = value
	}
	put.Config = api.ConfigMap(config)
	op, err := server.UpdateInstance(plan.InstanceName, put, etag)
	if err != nil {
		return meta.Machine{}, fmt.Errorf("update machine %s host override metadata: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return meta.Machine{}, fmt.Errorf("wait for machine %s metadata update: %w", plan.InstanceName, err)
	}
	return state, nil
}

func addPlanFromDelete(plan hostoverride.DeletePlan) hostoverride.AddPlan {
	return hostoverride.AddPlan{
		Reference:    plan.Reference,
		Tenant:       plan.Tenant,
		Machine:      plan.Machine,
		InstanceName: plan.InstanceName,
		StoragePool:  plan.StoragePool,
		CAVolume:     plan.CAVolume,
		Hostname:     plan.Hostname,
	}
}

func (s sdkHostOverrideServer) UseProject(name string) HostOverrideResourceServer {
	return sdkHostOverrideResourceServer{inner: s.inner.UseProject(name), projectName: name}
}

type sdkHostOverrideResourceServer struct {
	inner       incus.InstanceServer
	projectName string
}

func (s sdkHostOverrideResourceServer) GetInstances(instanceType api.InstanceType) ([]api.Instance, error) {
	return s.inner.GetInstances(instanceType)
}

func (s sdkHostOverrideResourceServer) GetInstance(name string) (*api.Instance, string, error) {
	return s.inner.GetInstance(name)
}

func (s sdkHostOverrideResourceServer) UpdateInstance(name string, instance api.InstancePut, ETag string) (incus.Operation, error) {
	return s.inner.UpdateInstance(name, instance, ETag)
}

func (s sdkHostOverrideResourceServer) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
	return s.inner.CreateInstanceFile(instanceName, path, args)
}

func (s sdkHostOverrideResourceServer) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	return getStorageVolumeFile(s.inner, s.projectName, pool, volumeType, volumeName, filePath)
}

func (s sdkHostOverrideResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	return s.inner.ExecInstance(instanceName, exec, args)
}

func updateMachineExtraSANs(server HostOverrideResourceServer, plan hostoverride.AddPlan) (meta.Machine, error) {
	instance, etag, err := server.GetInstance(plan.InstanceName)
	if err != nil {
		return meta.Machine{}, fmt.Errorf("get machine %s: %w", plan.InstanceName, err)
	}
	put := instance.Writable()
	config := map[string]string(put.Config)
	state, err := meta.ParseMachineConfig(config)
	if err != nil {
		return meta.Machine{}, fmt.Errorf("parse machine metadata for %s: %w", plan.InstanceName, err)
	}
	state.ExtraSANs = appendUnique(state.ExtraSANs, plan.ExtraSANs...)
	updated, err := meta.MachineConfig(state)
	if err != nil {
		return meta.Machine{}, err
	}
	for key, value := range updated {
		config[key] = value
	}
	put.Config = api.ConfigMap(config)
	op, err := server.UpdateInstance(plan.InstanceName, put, etag)
	if err != nil {
		return meta.Machine{}, fmt.Errorf("update machine %s host override metadata: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return meta.Machine{}, fmt.Errorf("wait for machine %s metadata update: %w", plan.InstanceName, err)
	}
	return state, nil
}

func writeHostOverrideMachineFiles(server HostOverrideResourceServer, plan hostoverride.AddPlan, state meta.Machine) error {
	certificateFiles, err := issueHostOverrideCertificateFiles(server, plan, state.ExtraSANs)
	if err != nil {
		return err
	}
	for _, directory := range []string{"/etc/caddy", "/etc/caddy/certs"} {
		err := server.CreateInstanceFile(plan.InstanceName, directory, incus.InstanceFileArgs{Type: "directory", Mode: 0o755})
		if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
			return fmt.Errorf("create machine config directory %s: %w", directory, err)
		}
	}
	hosts := append([]string{state.Name + "." + state.Project + "." + plan.Tenant.DNSSuffix}, state.ExtraSANs...)
	caddyFile := caddy.RenderMachineHosts(hosts, state.AppPort, machine.MachineCertPath, machine.MachineCertKeyPath)
	if err := server.CreateInstanceFile(plan.InstanceName, caddyFile.Path, incus.InstanceFileArgs{
		Content:   strings.NewReader(caddyFile.Content),
		Type:      "file",
		Mode:      caddyFile.Mode,
		WriteMode: "overwrite",
	}); err != nil {
		return fmt.Errorf("write machine Caddyfile %s: %w", caddyFile.Path, err)
	}
	for _, file := range certificateFiles {
		if err := server.CreateInstanceFile(plan.InstanceName, file.Path, incus.InstanceFileArgs{
			Content:   strings.NewReader(string(file.Content)),
			Type:      "file",
			Mode:      file.Mode,
			WriteMode: "overwrite",
		}); err != nil {
			return fmt.Errorf("write machine certificate file %s: %w", file.Path, err)
		}
	}
	return restartMachineCaddy(server, plan.InstanceName, "", "")
}

func issueHostOverrideCertificateFiles(server HostOverrideResourceServer, plan hostoverride.AddPlan, extraSANs []string) ([]machine.File, error) {
	caCertPEM, err := readHostOverrideCAFile(server, plan, tenant.TenantCACertPath)
	if err != nil {
		return nil, fmt.Errorf("read tenant CA certificate: %w", err)
	}
	caKeyPEM, err := readHostOverrideCAFile(server, plan, tenant.TenantCAKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read tenant CA private key: %w", err)
	}
	files, err := machine.IssueCertificateFilesWithExtraSANs(plan.Machine.Name, plan.Machine.Project, plan.Tenant.DNSSuffix, extraSANs, caCertPEM, caKeyPEM)
	if err != nil {
		return nil, err
	}
	return files, nil
}

func readHostOverrideCAFile(server HostOverrideResourceServer, plan hostoverride.AddPlan, path string) ([]byte, error) {
	content, _, err := server.GetStorageVolumeFile(plan.StoragePool, "custom", plan.CAVolume, path)
	if err != nil {
		return nil, err
	}
	defer content.Close()
	return io.ReadAll(content)
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
