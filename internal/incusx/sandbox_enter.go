package incusx

import (
	"context"
	"fmt"
	"os"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
	"golang.org/x/term"
)

type SandboxEnterServer interface {
	UseProject(name string) SandboxEnterResourceServer
}

type SandboxEnterResourceServer interface {
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}

type SandboxEnterer struct {
	Remote     string
	ConfigPath string
	Server     SandboxEnterServer
}

func NewSandboxEnterer(remote string) SandboxEnterer {
	return SandboxEnterer{Remote: remote}
}

func (e SandboxEnterer) EnterSandbox(ctx context.Context, plan sandbox.EnterPlan, session sandbox.EnterSession) error {
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
		server = sdkSandboxEnterServer{inner: instanceServer}
	}
	projectServer := server.UseProject(plan.Project.IncusName)
	exec := api.InstanceExecPost{
		Command:     plan.Command,
		Cwd:         plan.WorkingDir,
		Interactive: plan.Interactive,
		WaitForWS:   true,
	}
	if exec.Interactive {
		exec.RecordOutput = false
	} else {
		exec.RecordOutput = true
	}
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
		return fmt.Errorf("enter sandbox %s: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for sandbox %s session: %w", plan.InstanceName, err)
	}
	<-dataDone
	return nil
}

type sdkSandboxEnterServer struct {
	inner incus.InstanceServer
}

func (s sdkSandboxEnterServer) UseProject(name string) SandboxEnterResourceServer {
	return sdkSandboxEnterResourceServer{inner: s.inner.UseProject(name)}
}

type sdkSandboxEnterResourceServer struct {
	inner incus.InstanceServer
}

func (s sdkSandboxEnterResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	return s.inner.ExecInstance(instanceName, exec, args)
}
