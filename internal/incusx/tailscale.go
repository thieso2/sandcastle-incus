package incusx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/tailscale"
)

type TailscaleServer interface {
	UseProject(name string) TailscaleResourceServer
	GetProject(name string) (*api.Project, string, error)
	UpdateProject(name string, project api.ProjectPut, ETag string) error
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
	projectServer := server.UseProject(plan.Tenant.InfraProject)
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
	result, err := runTailscaleStatus(ctx, server, tailscale.StatusPlan{
		Reference:    plan.Reference,
		Tenant:       plan.Tenant,
		InstanceName: plan.InstanceName,
		Command:      []string{"tailscale", "status", "--json"},
	}, tailscale.RunSession{Stderr: session.Stderr})
	if err != nil {
		return err
	}
	if err := validateTailscaleUpResult(plan, result); err != nil {
		return err
	}
	return nil
}

func (m TailscaleManager) RunStatus(ctx context.Context, plan tailscale.StatusPlan, session tailscale.RunSession) (tailscale.StatusResult, error) {
	server, err := m.server()
	if err != nil {
		return tailscale.StatusResult{}, err
	}
	return runTailscaleStatus(ctx, server, plan, session)
}

func runTailscaleStatus(ctx context.Context, server TailscaleServer, plan tailscale.StatusPlan, session tailscale.RunSession) (tailscale.StatusResult, error) {
	projectServer := server.UseProject(plan.Tenant.InfraProject)
	var stdout bytes.Buffer
	dataDone := make(chan bool)
	op, err := projectServer.ExecInstance(plan.InstanceName, api.InstanceExecPost{
		Command:   plan.Command,
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		Stdout:   &stdout,
		Stderr:   writerOrDiscard(session.Stderr),
		DataDone: dataDone,
	})
	if err != nil {
		return tailscale.StatusResult{}, fmt.Errorf("run tailscale status in %s: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return tailscale.StatusResult{}, fmt.Errorf("wait for tailscale status in %s: %w", plan.InstanceName, err)
	}
	<-dataDone
	result, err := tailscale.ParseStatus(plan.Reference, plan.Tenant, stdout.Bytes(), time.Now().UTC())
	if err != nil {
		return tailscale.StatusResult{}, err
	}
	if err := updateTenantTailscale(server, plan.Tenant.IncusName, result.Tailscale); err != nil {
		if isRestrictedCertificateError(err) {
			return result, nil
		}
		return tailscale.StatusResult{}, err
	}
	return result, nil
}

func validateTailscaleUpResult(plan tailscale.UpPlan, result tailscale.StatusResult) error {
	if !strings.EqualFold(result.Tailscale.State, "running") {
		return fmt.Errorf("tailscale up in %s did not authenticate sidecar: state %s", plan.InstanceName, result.Tailscale.State)
	}
	if len(result.Tailscale.TailscaleIPs) == 0 {
		return fmt.Errorf("tailscale up in %s did not assign a Tailscale IP", plan.InstanceName)
	}
	return nil
}

func isRestrictedCertificateError(err error) bool {
	if api.StatusErrorCheck(err, http.StatusForbidden) {
		return true
	}
	return strings.Contains(err.Error(), "Certificate is restricted")
}

func (m TailscaleManager) RunDown(ctx context.Context, plan tailscale.DownPlan, session tailscale.RunSession) error {
	server, err := m.server()
	if err != nil {
		return err
	}
	projectServer := server.UseProject(plan.Tenant.InfraProject)
	dataDone := make(chan bool)
	op, err := projectServer.ExecInstance(plan.InstanceName, api.InstanceExecPost{
		Command:   plan.Command,
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		Stdout:   writerOrDiscard(session.Stdout),
		Stderr:   writerOrDiscard(session.Stderr),
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("run tailscale down in %s: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for tailscale down in %s: %w", plan.InstanceName, err)
	}
	<-dataDone
	return updateTenantTailscale(server, plan.Tenant.IncusName, meta.Tailscale{
		State:         "stopped",
		LastCheckedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

func (m TailscaleManager) server() (TailscaleServer, error) {
	if m.Server != nil {
		return m.Server, nil
	}
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
	return sdkTailscaleServer{inner: instanceServer}, nil
}

func updateTenantTailscale(server TailscaleServer, name string, state meta.Tailscale) error {
	projectState, etag, err := server.GetProject(name)
	if err != nil {
		return fmt.Errorf("get tenant %s: %w", name, err)
	}
	managed, err := meta.ParseTenantConfig(map[string]string(projectState.Config))
	if err != nil {
		return fmt.Errorf("parse tenant metadata for %s: %w", name, err)
	}
	managed.Tailscale = state
	config, err := meta.TenantConfig(managed)
	if err != nil {
		return err
	}
	put := projectState.Writable()
	if put.Config == nil {
		put.Config = api.ConfigMap{}
	}
	for key, value := range config {
		put.Config[key] = value
	}
	if err := server.UpdateProject(name, put, etag); err != nil {
		return fmt.Errorf("update tenant %s tailscale metadata: %w", name, err)
	}
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

func (s sdkTailscaleServer) GetProject(name string) (*api.Project, string, error) {
	return s.inner.GetProject(name)
}

func (s sdkTailscaleServer) UpdateProject(name string, project api.ProjectPut, etag string) error {
	return s.inner.UpdateProject(name, project, etag)
}

type sdkTailscaleResourceServer struct {
	inner incus.InstanceServer
}

func (s sdkTailscaleResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	return s.inner.ExecInstance(instanceName, exec, args)
}
