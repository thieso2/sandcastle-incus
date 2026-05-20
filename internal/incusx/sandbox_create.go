package incusx

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
)

type SandboxCreateServer interface {
	UseProject(name string) SandboxResourceServer
}

type SandboxResourceServer interface {
	GetInstance(name string) (*api.Instance, string, error)
	CreateInstance(instance api.InstancesPost) (incus.Operation, error)
	UpdateInstanceState(name string, state api.InstanceStatePut, ETag string) (incus.Operation, error)
	CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error
}

type SandboxCreator struct {
	Remote     string
	ConfigPath string
	Server     SandboxCreateServer
}

func NewSandboxCreator(remote string) SandboxCreator {
	return SandboxCreator{Remote: remote}
}

func (c SandboxCreator) CreateSandbox(ctx context.Context, plan sandbox.CreatePlan) error {
	server := c.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(c.ConfigPath)
		if err != nil {
			return fmt.Errorf("load Incus config: %w", err)
		}
		remote := c.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkSandboxServer{inner: instanceServer}
	}
	projectServer := server.UseProject(plan.Project.IncusName)
	instance, _, err := projectServer.GetInstance(plan.InstanceName)
	if err == nil {
		if plan.StartsByDefault && !instance.IsActive() {
			op, err := projectServer.UpdateInstanceState(plan.InstanceName, api.InstanceStatePut{Action: "start", Timeout: -1}, "")
			if err != nil {
				return fmt.Errorf("start sandbox %s: %w", plan.InstanceName, err)
			}
			if err := op.Wait(); err != nil {
				return err
			}
		}
		return ensureSandboxFiles(projectServer, plan)
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get sandbox %s: %w", plan.InstanceName, err)
	}
	op, err := projectServer.CreateInstance(sandboxRequest(plan))
	if err != nil {
		return fmt.Errorf("create sandbox %s: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return err
	}
	return ensureSandboxFiles(projectServer, plan)
}

func ensureSandboxFiles(server SandboxResourceServer, plan sandbox.CreatePlan) error {
	for _, directory := range []string{"/etc/caddy", "/etc/caddy/certs"} {
		err := server.CreateInstanceFile(plan.InstanceName, directory, incus.InstanceFileArgs{
			Type: "directory",
			Mode: 0o755,
		})
		if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
			return fmt.Errorf("create sandbox config directory %s: %w", directory, err)
		}
	}
	if err := server.CreateInstanceFile(plan.InstanceName, plan.CaddyFile.Path, incus.InstanceFileArgs{
		Content:   strings.NewReader(plan.CaddyFile.Content),
		Type:      "file",
		Mode:      plan.CaddyFile.Mode,
		WriteMode: "overwrite",
	}); err != nil {
		return fmt.Errorf("write sandbox Caddyfile %s: %w", plan.CaddyFile.Path, err)
	}
	for _, file := range plan.CertificateFiles {
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

func sandboxRequest(plan sandbox.CreatePlan) api.InstancesPost {
	return api.InstancesPost{
		Name:  plan.InstanceName,
		Type:  "container",
		Start: plan.StartsByDefault,
		Source: api.InstanceSource{
			Type:  "image",
			Alias: plan.ImageAlias,
		},
		InstancePut: api.InstancePut{
			Description: "Sandcastle sandbox " + plan.Reference,
			Config:      api.ConfigMap(plan.MetadataConfig),
			Devices:     sandboxDevicesMap(plan.Devices),
			Profiles:    []string{},
		},
	}
}

func sandboxDevicesMap(devices map[string]sandbox.Device) api.DevicesMap {
	output := make(api.DevicesMap, len(devices))
	for name, device := range devices {
		output[name] = map[string]string(device)
	}
	return output
}

type sdkSandboxServer struct {
	inner incus.InstanceServer
}

func (s sdkSandboxServer) UseProject(name string) SandboxResourceServer {
	return sdkSandboxResourceServer{inner: s.inner.UseProject(name)}
}

type sdkSandboxResourceServer struct {
	inner incus.InstanceServer
}

func (s sdkSandboxResourceServer) GetInstance(name string) (*api.Instance, string, error) {
	return s.inner.GetInstance(name)
}

func (s sdkSandboxResourceServer) CreateInstance(instance api.InstancesPost) (incus.Operation, error) {
	return s.inner.CreateInstance(instance)
}

func (s sdkSandboxResourceServer) UpdateInstanceState(name string, state api.InstanceStatePut, etag string) (incus.Operation, error) {
	return s.inner.UpdateInstanceState(name, state, etag)
}

func (s sdkSandboxResourceServer) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
	return s.inner.CreateInstanceFile(instanceName, path, args)
}
