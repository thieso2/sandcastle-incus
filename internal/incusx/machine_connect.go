package incusx

import (
	"context"
	"fmt"
	"os"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"golang.org/x/term"
)

type MachineConnectServer interface {
	UseProject(name string) MachineConnectResourceServer
}

type MachineConnectResourceServer interface {
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}

type MachineConnector struct {
	Remote     string
	ConfigPath string
	Server     MachineConnectServer
}

func NewMachineConnector(remote string) MachineConnector {
	return MachineConnector{Remote: remote}
}

func (e MachineConnector) ConnectMachine(ctx context.Context, plan machine.ConnectPlan, session machine.ConnectSession) error {
	server := e.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(e.ConfigPath)
		if err != nil {
			return fmt.Errorf("load Incus config: %w", err)
		}
		remote := e.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkMachineConnectServer{inner: instanceServer}
	}
	projectServer := server.UseProject(plan.Tenant.IncusName)
	exec := api.InstanceExecPost{
		Command:     plan.Command,
		Cwd:         plan.WorkingDir,
		User:        machine.DefaultLinuxUID,
		Group:       machine.DefaultLinuxGID,
		Interactive: plan.Interactive,
		WaitForWS:   true,
		Environment: map[string]string{
			"HOME": "/home/" + plan.LinuxUser,
			"USER": plan.LinuxUser,
		},
	}
	exec.RecordOutput = false
	if exec.Interactive {
		if file, ok := session.Stdin.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
			width, height, err := term.GetSize(int(file.Fd()))
			if err == nil {
				exec.Width = width
				exec.Height = height
			}
			oldState, err := term.MakeRaw(int(file.Fd()))
			if err == nil {
				defer term.Restore(int(file.Fd()), oldState)
			}
		}
	}
	dataDone := make(chan bool)
	op, err := projectServer.ExecInstance(plan.InstanceName, exec, &incus.InstanceExecArgs{
		Stdin:    session.Stdin,
		Stdout:   session.Stdout,
		Stderr:   session.Stderr,
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("connect to machine %s: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for machine %s session: %w", plan.InstanceName, err)
	}
	<-dataDone
	return nil
}

type sdkMachineConnectServer struct {
	inner incus.InstanceServer
}

func (s sdkMachineConnectServer) UseProject(name string) MachineConnectResourceServer {
	return sdkMachineConnectResourceServer{inner: s.inner.UseProject(name)}
}

type sdkMachineConnectResourceServer struct {
	inner incus.InstanceServer
}

func (s sdkMachineConnectResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	return s.inner.ExecInstance(instanceName, exec, args)
}
