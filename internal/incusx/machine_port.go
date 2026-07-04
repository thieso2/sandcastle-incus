package incusx

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
)

type MachinePortServer interface {
	UseProject(name string) MachinePortResourceServer
}

type MachinePortResourceServer interface {
	GetInstance(name string) (*api.Instance, string, error)
	UpdateInstance(name string, instance api.InstancePut, ETag string) (incus.Operation, error)
	CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}

type MachinePortSetter struct {
	Remote     string
	ConfigPath string
	Server     MachinePortServer
}

func NewMachinePortSetter(remote string) MachinePortSetter {
	return MachinePortSetter{Remote: remote}
}

func (s MachinePortSetter) SetAppPort(ctx context.Context, plan machine.PortSetPlan) error {
	server := s.Server
	if server == nil {
		loaded, err := LoadCLIConfig(s.ConfigPath)
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
		server = sdkMachinePortServer{inner: instanceServer}
	}
	projectServer := server.UseProject(plan.Tenant.IncusName)
	instance, etag, err := projectServer.GetInstance(plan.InstanceName)
	if err != nil {
		return fmt.Errorf("get machine %s: %w", plan.InstanceName, err)
	}
	put := instance.Writable()
	config := map[string]string(put.Config)
	state, err := meta.ParseMachineConfig(config)
	if err != nil {
		return fmt.Errorf("parse machine metadata for %s: %w", plan.InstanceName, err)
	}
	state.AppPort = plan.AppPort
	updated, err := meta.MachineConfig(state)
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
		return fmt.Errorf("update machine %s app port: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return err
	}
	if err := writeMachineCaddyfile(projectServer, plan); err != nil {
		return err
	}
	return restartMachineCaddy(projectServer, plan.InstanceName, "", "")
}

func writeMachineCaddyfile(server MachinePortResourceServer, plan machine.PortSetPlan) error {
	err := server.CreateInstanceFile(plan.InstanceName, "/etc/caddy", incus.InstanceFileArgs{
		Type: "directory",
		Mode: 0o755,
	})
	if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
		return fmt.Errorf("create machine Caddy config directory: %w", err)
	}
	if err := server.CreateInstanceFile(plan.InstanceName, plan.CaddyFile.Path, incus.InstanceFileArgs{
		Content:   strings.NewReader(plan.CaddyFile.Content),
		Type:      "file",
		Mode:      plan.CaddyFile.Mode,
		WriteMode: "overwrite",
	}); err != nil {
		return fmt.Errorf("write machine Caddyfile %s: %w", plan.CaddyFile.Path, err)
	}
	return nil
}

type sdkMachinePortServer struct {
	inner incus.InstanceServer
}

func (s sdkMachinePortServer) UseProject(name string) MachinePortResourceServer {
	return sdkMachinePortResourceServer{inner: s.inner.UseProject(name)}
}

type sdkMachinePortResourceServer struct {
	inner incus.InstanceServer
}

func (s sdkMachinePortResourceServer) GetInstance(name string) (*api.Instance, string, error) {
	return s.inner.GetInstance(name)
}

func (s sdkMachinePortResourceServer) UpdateInstance(name string, instance api.InstancePut, etag string) (incus.Operation, error) {
	return s.inner.UpdateInstance(name, instance, etag)
}

func (s sdkMachinePortResourceServer) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
	return s.inner.CreateInstanceFile(instanceName, path, args)
}

func (s sdkMachinePortResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	return s.inner.ExecInstance(instanceName, exec, args)
}
