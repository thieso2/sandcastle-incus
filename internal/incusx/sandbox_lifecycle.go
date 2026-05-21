package incusx

import (
	"context"
	"fmt"
	"net/http"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
)

type SandboxLifecycleServer interface {
	UseProject(name string) SandboxLifecycleResourceServer
}

type SandboxLifecycleResourceServer interface {
	UpdateInstanceState(name string, state api.InstanceStatePut, ETag string) (incus.Operation, error)
	DeleteInstance(name string) (incus.Operation, error)
}

type SandboxController struct {
	Remote     string
	ConfigPath string
	Server     SandboxLifecycleServer
}

func NewSandboxController(remote string) SandboxController {
	return SandboxController{Remote: remote}
}

func (c SandboxController) ApplyLifecycle(ctx context.Context, plan sandbox.LifecyclePlan) error {
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
		server = sdkSandboxLifecycleServer{inner: instanceServer}
	}
	projectServer := server.UseProject(plan.Project.IncusName)
	switch plan.Action {
	case sandbox.ActionStart:
		op, err := projectServer.UpdateInstanceState(plan.InstanceName, api.InstanceStatePut{Action: "start", Timeout: -1}, "")
		return waitOperation(op, err, "start sandbox "+plan.InstanceName)
	case sandbox.ActionStop:
		op, err := projectServer.UpdateInstanceState(plan.InstanceName, api.InstanceStatePut{Action: "stop", Timeout: -1, Force: true}, "")
		return waitOperation(op, err, "stop sandbox "+plan.InstanceName)
	case sandbox.ActionRestart:
		op, err := projectServer.UpdateInstanceState(plan.InstanceName, api.InstanceStatePut{Action: "restart", Timeout: -1, Force: true}, "")
		return waitOperation(op, err, "restart sandbox "+plan.InstanceName)
	case sandbox.ActionRemove:
		stopOp, stopErr := projectServer.UpdateInstanceState(plan.InstanceName, api.InstanceStatePut{Action: "stop", Timeout: -1, Force: true}, "")
		if stopErr == nil {
			if err := stopOp.Wait(); err != nil {
				return fmt.Errorf("stop sandbox %s before remove: %w", plan.InstanceName, err)
			}
		}
		op, err := projectServer.DeleteInstance(plan.InstanceName)
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}
		return waitOperation(op, err, "remove sandbox "+plan.InstanceName)
	default:
		return fmt.Errorf("unsupported sandbox action %q", plan.Action)
	}
}

func waitOperation(op incus.Operation, err error, action string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for %s: %w", action, err)
	}
	return nil
}

type sdkSandboxLifecycleServer struct {
	inner incus.InstanceServer
}

func (s sdkSandboxLifecycleServer) UseProject(name string) SandboxLifecycleResourceServer {
	return sdkSandboxLifecycleResourceServer{inner: s.inner.UseProject(name)}
}

type sdkSandboxLifecycleResourceServer struct {
	inner incus.InstanceServer
}

func (s sdkSandboxLifecycleResourceServer) UpdateInstanceState(name string, state api.InstanceStatePut, etag string) (incus.Operation, error) {
	return s.inner.UpdateInstanceState(name, state, etag)
}

func (s sdkSandboxLifecycleResourceServer) DeleteInstance(name string) (incus.Operation, error) {
	return s.inner.DeleteInstance(name)
}
