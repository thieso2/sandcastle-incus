package incusx

import (
	"context"
	"fmt"
	"strconv"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
)

type SandboxPortServer interface {
	UseProject(name string) SandboxPortResourceServer
}

type SandboxPortResourceServer interface {
	GetInstance(name string) (*api.Instance, string, error)
	UpdateInstance(name string, instance api.InstancePut, ETag string) (incus.Operation, error)
}

type SandboxPortSetter struct {
	Remote     string
	ConfigPath string
	Server     SandboxPortServer
}

func NewSandboxPortSetter(remote string) SandboxPortSetter {
	return SandboxPortSetter{Remote: remote}
}

func (s SandboxPortSetter) SetAppPort(ctx context.Context, plan sandbox.PortSetPlan) error {
	server := s.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(s.ConfigPath)
		if err != nil {
			return fmt.Errorf("load Incus config: %w", err)
		}
		remote := s.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkSandboxPortServer{inner: instanceServer}
	}
	projectServer := server.UseProject(plan.Project.IncusName)
	instance, etag, err := projectServer.GetInstance(plan.InstanceName)
	if err != nil {
		return fmt.Errorf("get sandbox %s: %w", plan.InstanceName, err)
	}
	put := instance.Writable()
	config := map[string]string(put.Config)
	state, err := meta.ParseSandboxConfig(config)
	if err != nil {
		return fmt.Errorf("parse sandbox metadata for %s: %w", plan.InstanceName, err)
	}
	state.AppPort = plan.AppPort
	updated, err := meta.SandboxConfig(state)
	if err != nil {
		return err
	}
	for key, value := range updated {
		config[key] = value
	}
	config[meta.KeyAppPort] = strconv.Itoa(plan.AppPort)
	put.Config = api.ConfigMap(config)
	op, err := projectServer.UpdateInstance(plan.InstanceName, put, etag)
	if err != nil {
		return fmt.Errorf("update sandbox %s app port: %w", plan.InstanceName, err)
	}
	return op.Wait()
}

type sdkSandboxPortServer struct {
	inner incus.InstanceServer
}

func (s sdkSandboxPortServer) UseProject(name string) SandboxPortResourceServer {
	return sdkSandboxPortResourceServer{inner: s.inner.UseProject(name)}
}

type sdkSandboxPortResourceServer struct {
	inner incus.InstanceServer
}

func (s sdkSandboxPortResourceServer) GetInstance(name string) (*api.Instance, string, error) {
	return s.inner.GetInstance(name)
}

func (s sdkSandboxPortResourceServer) UpdateInstance(name string, instance api.InstancePut, etag string) (incus.Operation, error) {
	return s.inner.UpdateInstance(name, instance, etag)
}
