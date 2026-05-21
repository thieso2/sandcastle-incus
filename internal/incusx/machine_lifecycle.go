package incusx

import (
	"context"
	"fmt"
	"net/http"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
)

type MachineLifecycleServer interface {
	UseProject(name string) MachineLifecycleResourceServer
}

type MachineLifecycleResourceServer interface {
	UpdateInstanceState(name string, state api.InstanceStatePut, ETag string) (incus.Operation, error)
	DeleteInstance(name string) (incus.Operation, error)
}

type MachineController struct {
	Remote     string
	ConfigPath string
	Server     MachineLifecycleServer
}

func NewMachineController(remote string) MachineController {
	return MachineController{Remote: remote}
}

func (c MachineController) ApplyLifecycle(ctx context.Context, plan machine.LifecyclePlan) error {
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
		server = sdkMachineLifecycleServer{inner: instanceServer}
	}
	projectServer := server.UseProject(plan.Tenant.IncusName)
	switch plan.Action {
	case machine.ActionStart:
		op, err := projectServer.UpdateInstanceState(plan.InstanceName, api.InstanceStatePut{Action: "start", Timeout: -1}, "")
		return waitOperation(op, err, "start machine "+plan.InstanceName)
	case machine.ActionStop:
		op, err := projectServer.UpdateInstanceState(plan.InstanceName, api.InstanceStatePut{Action: "stop", Timeout: -1, Force: true}, "")
		return waitOperation(op, err, "stop machine "+plan.InstanceName)
	case machine.ActionRestart:
		op, err := projectServer.UpdateInstanceState(plan.InstanceName, api.InstanceStatePut{Action: "restart", Timeout: -1, Force: true}, "")
		return waitOperation(op, err, "restart machine "+plan.InstanceName)
	case machine.ActionDelete:
		stopOp, stopErr := projectServer.UpdateInstanceState(plan.InstanceName, api.InstanceStatePut{Action: "stop", Timeout: -1, Force: true}, "")
		if stopErr == nil {
			if err := stopOp.Wait(); err != nil {
				return fmt.Errorf("stop machine %s before delete: %w", plan.InstanceName, err)
			}
		}
		op, err := projectServer.DeleteInstance(plan.InstanceName)
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}
		return waitOperation(op, err, "delete machine "+plan.InstanceName)
	default:
		return fmt.Errorf("unsupported machine action %q", plan.Action)
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

type sdkMachineLifecycleServer struct {
	inner incus.InstanceServer
}

func (s sdkMachineLifecycleServer) UseProject(name string) MachineLifecycleResourceServer {
	return sdkMachineLifecycleResourceServer{inner: s.inner.UseProject(name)}
}

type sdkMachineLifecycleResourceServer struct {
	inner incus.InstanceServer
}

func (s sdkMachineLifecycleResourceServer) UpdateInstanceState(name string, state api.InstanceStatePut, etag string) (incus.Operation, error) {
	return s.inner.UpdateInstanceState(name, state, etag)
}

func (s sdkMachineLifecycleResourceServer) DeleteInstance(name string) (incus.Operation, error) {
	return s.inner.DeleteInstance(name)
}
