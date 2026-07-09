package incusx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
)

type MachineLifecycleServer interface {
	UseProject(name string) MachineLifecycleResourceServer
}

type MachineLifecycleResourceServer interface {
	UpdateInstanceState(name string, state api.InstanceStatePut, ETag string) (incus.Operation, error)
	DeleteInstance(name string) (incus.Operation, error)
	GetInstance(name string) (*api.Instance, string, error)
	GetNetwork(name string) (*api.Network, string, error)
	ExecInstance(name string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}

type MachineController struct {
	Remote     string
	ConfigPath string
	Server     MachineLifecycleServer
	// Warn receives best-effort warnings (e.g. self-heal failures) that must
	// not abort the requested lifecycle action. Nil discards them.
	Warn io.Writer
}

func NewMachineController(remote string) MachineController {
	return MachineController{Remote: remote, Warn: os.Stderr}
}

func (c MachineController) ApplyLifecycle(ctx context.Context, plan machine.LifecyclePlan) error {
	server := c.Server
	if server == nil {
		loaded, err := LoadCLIConfig(c.ConfigPath)
		if err != nil {
			return fmt.Errorf("load Incus config: %w", err)
		}
		remote := c.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := connectInstanceServer(loaded, remote)
		if err != nil {
			return fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkMachineLifecycleServer{inner: instanceServer}
	}
	projectServer := server.UseProject(plan.Tenant.IncusName)
	switch plan.Action {
	case machine.ActionStart:
		op, err := projectServer.UpdateInstanceState(plan.InstanceName, api.InstanceStatePut{Action: "start", Timeout: -1}, "")
		if err := waitOperation(op, err, "start machine "+plan.InstanceName); err != nil {
			return err
		}
		c.healMachineNetwork(projectServer, plan.InstanceName)
		return nil
	case machine.ActionStop:
		op, err := projectServer.UpdateInstanceState(plan.InstanceName, api.InstanceStatePut{Action: "stop", Timeout: -1, Force: true}, "")
		return waitOperation(op, err, "stop machine "+plan.InstanceName)
	case machine.ActionRestart:
		op, err := projectServer.UpdateInstanceState(plan.InstanceName, api.InstanceStatePut{Action: "restart", Timeout: -1, Force: true}, "")
		if err := waitOperation(op, err, "restart machine "+plan.InstanceName); err != nil {
			return err
		}
		c.healMachineNetwork(projectServer, plan.InstanceName)
		return nil
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

// healMachineNetwork best-effort (re)installs the in-guest static-network unit
// after a machine starts. Sandcastle images have no DHCP client, so a machine
// created before that unit existed comes up RUNNING but with no eth0 IP after a
// host reboot (the create-time `ip addr replace` does not survive a reboot).
// Reinstalling the unit is idempotent for machines that already have it, and
// heals pre-fix machines on their next start/restart. Failures are warned, never
// fatal: a heal hiccup must not turn a working `start` into an error.
func (c MachineController) healMachineNetwork(server MachineLifecycleResourceServer, instanceName string) {
	if err := ensureMachineNetwork(server, instanceName); err != nil {
		if c.Warn != nil {
			fmt.Fprintf(c.Warn, "warning: could not ensure static network on %s: %v\n", instanceName, err)
		}
	}
}

func ensureMachineNetwork(server MachineLifecycleResourceServer, instanceName string) error {
	inst, _, err := server.GetInstance(instanceName)
	if err != nil {
		return fmt.Errorf("read instance: %w", err)
	}
	eth0, ok := inst.Devices["eth0"]
	if !ok {
		return nil // not a Sandcastle tenant NIC; nothing to heal
	}
	ip, parent := eth0["ipv4.address"], eth0["parent"]
	if ip == "" || parent == "" {
		return nil // no declared static IP; can't know what to apply
	}
	network, _, err := server.GetNetwork(parent)
	if err != nil {
		return fmt.Errorf("read network %s: %w", parent, err)
	}
	prefix, err := netip.ParsePrefix(network.Config["ipv4.address"])
	if err != nil {
		return fmt.Errorf("parse network %s ipv4.address %q: %w", parent, network.Config["ipv4.address"], err)
	}
	ipWithPrefix := fmt.Sprintf("%s/%d", ip, prefix.Bits())
	gateway := prefix.Addr().String()

	var stderr bytes.Buffer
	dataDone := make(chan bool)
	op, err := server.ExecInstance(instanceName, api.InstanceExecPost{
		Command:   []string{"/bin/sh", "-se"},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(machineNetworkHealScript(ipWithPrefix, gateway)),
		Stderr:   &stderr,
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("exec network heal: %w", err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("network heal (stderr: %s): %w", strings.TrimSpace(stderr.String()), err)
	}
	<-dataDone
	return nil
}

// machineNetworkHealScript wraps appendMachineNetworkConfigScript (shared with
// the create path) into a self-contained shell script. The create script
// defines step(); supply a no-op so the shared fragment runs standalone.
func machineNetworkHealScript(ipWithPrefix, gateway string) string {
	var script strings.Builder
	script.WriteString("set -eu\n")
	script.WriteString("step() { :; }\n")
	appendMachineNetworkConfigScript(&script, ipWithPrefix, gateway)
	return script.String()
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

func (s sdkMachineLifecycleResourceServer) GetInstance(name string) (*api.Instance, string, error) {
	return s.inner.GetInstance(name)
}

func (s sdkMachineLifecycleResourceServer) GetNetwork(name string) (*api.Network, string, error) {
	return s.inner.GetNetwork(name)
}

func (s sdkMachineLifecycleResourceServer) ExecInstance(name string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	return s.inner.ExecInstance(name, exec, args)
}
