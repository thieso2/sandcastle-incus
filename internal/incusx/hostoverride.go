package incusx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/caddy"
	"github.com/thieso2/sandcastle-incus/internal/hostoverride"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
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
}

type HostOverrideManager struct {
	Remote     string
	ConfigPath string
	Server     HostOverrideServer
}

func NewHostOverrideManager(remote string) HostOverrideManager {
	return HostOverrideManager{Remote: remote}
}

func (m HostOverrideManager) FindSandbox(ctx context.Context, summary project.Summary, name string) (meta.Sandbox, error) {
	sandboxes, err := m.ListSandboxes(ctx, summary)
	if err != nil {
		return meta.Sandbox{}, err
	}
	for _, sandbox := range sandboxes {
		if sandbox.Name == name {
			return sandbox, nil
		}
	}
	return meta.Sandbox{}, fmt.Errorf("Sandcastle sandbox %s/%s not found", summary.Owner+"/"+summary.Name, name)
}

func (m HostOverrideManager) ListSandboxes(ctx context.Context, summary project.Summary) ([]meta.Sandbox, error) {
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
	sandboxes := []meta.Sandbox{}
	for _, instance := range instances {
		if instance.Config[meta.KeyKind] != meta.KindSandbox {
			continue
		}
		sandbox, err := meta.ParseSandboxConfig(map[string]string(instance.Config))
		if err != nil {
			return nil, fmt.Errorf("parse sandbox metadata for %s: %w", instance.Name, err)
		}
		sandboxes = append(sandboxes, sandbox)
	}
	return sandboxes, nil
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
	projectServer := server.UseProject(plan.Project.IncusName)
	updatedSandbox, err := updateSandboxExtraSANs(projectServer, plan)
	if err != nil {
		return err
	}
	return writeHostOverrideSandboxFiles(projectServer, plan, updatedSandbox)
}

func (m HostOverrideManager) Remove(ctx context.Context, plan hostoverride.RemovePlan) error {
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
	projectServer := server.UseProject(plan.Project.IncusName)
	updatedSandbox, err := removeSandboxExtraSAN(projectServer, plan)
	if err != nil {
		return err
	}
	return writeHostOverrideSandboxFiles(projectServer, addPlanFromRemove(plan), updatedSandbox)
}

type sdkHostOverrideServer struct {
	inner incus.InstanceServer
}

func removeSandboxExtraSAN(server HostOverrideResourceServer, plan hostoverride.RemovePlan) (meta.Sandbox, error) {
	instance, etag, err := server.GetInstance(plan.InstanceName)
	if err != nil {
		return meta.Sandbox{}, fmt.Errorf("get sandbox %s: %w", plan.InstanceName, err)
	}
	put := instance.Writable()
	config := map[string]string(put.Config)
	state, err := meta.ParseSandboxConfig(config)
	if err != nil {
		return meta.Sandbox{}, fmt.Errorf("parse sandbox metadata for %s: %w", plan.InstanceName, err)
	}
	state.ExtraSANs = removeValue(state.ExtraSANs, plan.Hostname)
	updated, err := meta.SandboxConfig(state)
	if err != nil {
		return meta.Sandbox{}, err
	}
	for key, value := range updated {
		config[key] = value
	}
	put.Config = api.ConfigMap(config)
	op, err := server.UpdateInstance(plan.InstanceName, put, etag)
	if err != nil {
		return meta.Sandbox{}, fmt.Errorf("update sandbox %s host override metadata: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return meta.Sandbox{}, fmt.Errorf("wait for sandbox %s metadata update: %w", plan.InstanceName, err)
	}
	return state, nil
}

func addPlanFromRemove(plan hostoverride.RemovePlan) hostoverride.AddPlan {
	return hostoverride.AddPlan{
		Reference:    plan.Reference,
		Project:      plan.Project,
		Sandbox:      plan.Sandbox,
		InstanceName: plan.InstanceName,
		StoragePool:  plan.StoragePool,
		CAVolume:     plan.CAVolume,
		Hostname:     plan.Hostname,
	}
}

func (s sdkHostOverrideServer) UseProject(name string) HostOverrideResourceServer {
	return s.inner.UseProject(name)
}

func updateSandboxExtraSANs(server HostOverrideResourceServer, plan hostoverride.AddPlan) (meta.Sandbox, error) {
	instance, etag, err := server.GetInstance(plan.InstanceName)
	if err != nil {
		return meta.Sandbox{}, fmt.Errorf("get sandbox %s: %w", plan.InstanceName, err)
	}
	put := instance.Writable()
	config := map[string]string(put.Config)
	state, err := meta.ParseSandboxConfig(config)
	if err != nil {
		return meta.Sandbox{}, fmt.Errorf("parse sandbox metadata for %s: %w", plan.InstanceName, err)
	}
	state.ExtraSANs = appendUnique(state.ExtraSANs, plan.ExtraSANs...)
	updated, err := meta.SandboxConfig(state)
	if err != nil {
		return meta.Sandbox{}, err
	}
	for key, value := range updated {
		config[key] = value
	}
	put.Config = api.ConfigMap(config)
	op, err := server.UpdateInstance(plan.InstanceName, put, etag)
	if err != nil {
		return meta.Sandbox{}, fmt.Errorf("update sandbox %s host override metadata: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return meta.Sandbox{}, fmt.Errorf("wait for sandbox %s metadata update: %w", plan.InstanceName, err)
	}
	return state, nil
}

func writeHostOverrideSandboxFiles(server HostOverrideResourceServer, plan hostoverride.AddPlan, state meta.Sandbox) error {
	certificateFiles, err := issueHostOverrideCertificateFiles(server, plan, state.ExtraSANs)
	if err != nil {
		return err
	}
	for _, directory := range []string{"/etc/caddy", "/etc/caddy/certs"} {
		err := server.CreateInstanceFile(plan.InstanceName, directory, incus.InstanceFileArgs{Type: "directory", Mode: 0o755})
		if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
			return fmt.Errorf("create sandbox config directory %s: %w", directory, err)
		}
	}
	hosts := append([]string{state.Name + "." + plan.Project.Domain}, state.ExtraSANs...)
	caddyFile := caddy.RenderSandboxHosts(hosts, state.AppPort, sandbox.SandboxCertPath, sandbox.SandboxCertKeyPath)
	if err := server.CreateInstanceFile(plan.InstanceName, caddyFile.Path, incus.InstanceFileArgs{
		Content:   strings.NewReader(caddyFile.Content),
		Type:      "file",
		Mode:      caddyFile.Mode,
		WriteMode: "overwrite",
	}); err != nil {
		return fmt.Errorf("write sandbox Caddyfile %s: %w", caddyFile.Path, err)
	}
	for _, file := range certificateFiles {
		if err := server.CreateInstanceFile(plan.InstanceName, file.Path, incus.InstanceFileArgs{
			Content:   strings.NewReader(string(file.Content)),
			Type:      "file",
			Mode:      file.Mode,
			WriteMode: "overwrite",
		}); err != nil {
			return fmt.Errorf("write sandbox certificate file %s: %w", file.Path, err)
		}
	}
	return nil
}

func issueHostOverrideCertificateFiles(server HostOverrideResourceServer, plan hostoverride.AddPlan, extraSANs []string) ([]sandbox.File, error) {
	caCertPEM, err := readHostOverrideCAFile(server, plan, project.ProjectCACertPath)
	if err != nil {
		return nil, fmt.Errorf("read project CA certificate: %w", err)
	}
	caKeyPEM, err := readHostOverrideCAFile(server, plan, project.ProjectCAKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read project CA private key: %w", err)
	}
	files, err := sandbox.IssueCertificateFilesWithExtraSANs(plan.Sandbox.Name, plan.Project.Domain, extraSANs, caCertPEM, caKeyPEM)
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
