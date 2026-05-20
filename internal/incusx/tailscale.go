package incusx

import (
	"context"
	"fmt"
	"io"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/tailscale"
)

type TailscaleServer interface {
	UseProject(name string) TailscaleResourceServer
}

type TailscaleResourceServer interface {
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}

type TailscaleManager struct {
	Remote     string
	ConfigPath string
	Server     TailscaleServer
}

func NewTailscaleManager(remote string) TailscaleManager {
	return TailscaleManager{Remote: remote}
}

func (m TailscaleManager) RunUp(ctx context.Context, plan tailscale.UpPlan, session tailscale.RunSession) error {
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
		server = sdkTailscaleServer{inner: instanceServer}
	}
	projectServer := server.UseProject(plan.Project.IncusName)
	dataDone := make(chan bool)
	op, err := projectServer.ExecInstance(plan.InstanceName, api.InstanceExecPost{
		Command:   tailscale.ExecCommand(plan),
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		Stdout:   writerOrDiscard(session.Stdout),
		Stderr:   writerOrDiscard(session.Stderr),
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("run tailscale up in %s: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for tailscale up in %s: %w", plan.InstanceName, err)
	}
	<-dataDone
	return nil
}

func writerOrDiscard(writer io.Writer) io.Writer {
	if writer == nil {
		return io.Discard
	}
	return writer
}

type sdkTailscaleServer struct {
	inner incus.InstanceServer
}

func (s sdkTailscaleServer) UseProject(name string) TailscaleResourceServer {
	return sdkTailscaleResourceServer{inner: s.inner.UseProject(name)}
}

type sdkTailscaleResourceServer struct {
	inner incus.InstanceServer
}

func (s sdkTailscaleResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	return s.inner.ExecInstance(instanceName, exec, args)
}
