package incusx

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type DNSServer interface {
	UseProject(name string) DNSResourceServer
}

type DNSResourceServer interface {
	GetInstances(instanceType api.InstanceType) ([]api.Instance, error)
	CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}

type DNSManager struct {
	Remote     string
	ConfigPath string
	Server     DNSServer
}

func NewDNSManager(remote string) DNSManager {
	return DNSManager{Remote: remote}
}

func (m DNSManager) Apply(ctx context.Context, summary dns.Tenant) (dns.ApplyResult, error) {
	server := m.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(m.ConfigPath)
		if err != nil {
			return dns.ApplyResult{}, fmt.Errorf("load Incus config: %w", err)
		}
		remote := m.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return dns.ApplyResult{}, fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkDNSServer{inner: instanceServer}
	}
	mainServer := server.UseProject(summary.IncusName)
	infraProject := summary.InfraProject
	if infraProject == "" {
		infraProject = naming.TenantInfraIncusProjectName(summary.IncusName)
	}
	infraServer := server.UseProject(infraProject)
	machines, err := listMachineMetadata(mainServer)
	if err != nil {
		return dns.ApplyResult{}, err
	}
	result, err := dns.PlanApply(summary, machines)
	if err != nil {
		return dns.ApplyResult{}, err
	}
	if err := writeDNSFiles(infraServer, result.Files); err != nil {
		return dns.ApplyResult{}, err
	}
	if err := restartCoreDNS(infraServer); err != nil {
		return dns.ApplyResult{}, err
	}
	return result, nil
}

func listMachineMetadata(server DNSResourceServer) ([]meta.Machine, error) {
	instances, err := server.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		return nil, fmt.Errorf("list tenant instances: %w", err)
	}
	machines := []meta.Machine{}
	for _, instance := range instances {
		if instance.Config[meta.KeyKind] != meta.KindMachine {
			continue
		}
		machine, err := meta.ParseMachineConfig(map[string]string(instance.Config))
		if err != nil {
			return nil, fmt.Errorf("parse machine metadata for %s: %w", instance.Name, err)
		}
		machine.Running = instance.IsActive()
		machines = append(machines, machine)
	}
	return machines, nil
}

func writeDNSFiles(server DNSResourceServer, files []dns.File) error {
	for _, directory := range []string{"/etc/coredns", "/etc/coredns/zones"} {
		err := server.CreateInstanceFile(tenant.DNSName, directory, incus.InstanceFileArgs{Type: "directory", Mode: 0o755})
		if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
			return fmt.Errorf("create DNS config directory %s: %w", directory, err)
		}
	}
	for _, file := range files {
		if err := server.CreateInstanceFile(tenant.DNSName, file.Path, incus.InstanceFileArgs{
			Content:   strings.NewReader(file.Content),
			Type:      "file",
			Mode:      file.Mode,
			WriteMode: "overwrite",
		}); err != nil {
			return fmt.Errorf("write DNS file %s: %w", file.Path, err)
		}
	}
	return nil
}

type sdkDNSServer struct {
	inner incus.InstanceServer
}

func (s sdkDNSServer) UseProject(name string) DNSResourceServer {
	return sdkDNSResourceServer{inner: s.inner.UseProject(name)}
}

type sdkDNSResourceServer struct {
	inner incus.InstanceServer
}

func (s sdkDNSResourceServer) GetInstances(instanceType api.InstanceType) ([]api.Instance, error) {
	return s.inner.GetInstances(instanceType)
}

func (s sdkDNSResourceServer) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
	return s.inner.CreateInstanceFile(instanceName, path, args)
}

func (s sdkDNSResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	return s.inner.ExecInstance(instanceName, exec, args)
}
