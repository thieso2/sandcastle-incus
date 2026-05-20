package incusx

import (
	"context"
	"fmt"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/hostoverride"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type HostOverrideServer interface {
	UseProject(name string) HostOverrideResourceServer
}

type HostOverrideResourceServer interface {
	GetInstances(instanceType api.InstanceType) ([]api.Instance, error)
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
	server := m.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(m.ConfigPath)
		if err != nil {
			return meta.Sandbox{}, fmt.Errorf("load Incus config: %w", err)
		}
		remote := m.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return meta.Sandbox{}, fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkHostOverrideServer{inner: instanceServer}
	}
	projectServer := server.UseProject(summary.IncusName)
	instances, err := projectServer.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		return meta.Sandbox{}, fmt.Errorf("list project instances: %w", err)
	}
	for _, instance := range instances {
		if instance.Config[meta.KeyKind] != meta.KindSandbox {
			continue
		}
		sandbox, err := meta.ParseSandboxConfig(map[string]string(instance.Config))
		if err != nil {
			return meta.Sandbox{}, fmt.Errorf("parse sandbox metadata for %s: %w", instance.Name, err)
		}
		if sandbox.Name == name {
			return sandbox, nil
		}
	}
	return meta.Sandbox{}, fmt.Errorf("Sandcastle sandbox %s/%s not found", summary.Owner+"/"+summary.Name, name)
}

func (m HostOverrideManager) Add(ctx context.Context, plan hostoverride.AddPlan) error {
	return fmt.Errorf("host override apply is not implemented yet; rerun with --dry-run to inspect the planned /etc/hosts entry and certificate SAN")
}

type sdkHostOverrideServer struct {
	inner incus.InstanceServer
}

func (s sdkHostOverrideServer) UseProject(name string) HostOverrideResourceServer {
	return s.inner.UseProject(name)
}
