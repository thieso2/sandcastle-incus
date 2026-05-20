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

func (m DNSManager) Apply(ctx context.Context, summary dns.Project) (dns.ApplyResult, error) {
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
	projectServer := server.UseProject(summary.IncusName)
	sandboxes, err := listSandboxMetadata(projectServer)
	if err != nil {
		return dns.ApplyResult{}, err
	}
	result, err := dns.PlanApply(summary, sandboxes)
	if err != nil {
		return dns.ApplyResult{}, err
	}
	if err := writeDNSFiles(projectServer, result.Files); err != nil {
		return dns.ApplyResult{}, err
	}
	if err := restartCoreDNS(projectServer); err != nil {
		return dns.ApplyResult{}, err
	}
	return result, nil
}

func listSandboxMetadata(server DNSResourceServer) ([]meta.Sandbox, error) {
	instances, err := server.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		return nil, fmt.Errorf("list project instances: %w", err)
	}
	sandboxes := []meta.Sandbox{}
	for _, instance := range instances {
		if instance.Config[meta.KeyKind] != meta.KindSandbox {
			continue
		}
		sandbox, err := meta.ParseSandboxConfig(map[string]string(instance.Config))
		if err != nil {
			return nil, fmt.Errorf("parse sandbox metadata for %s: %w", instance.Name, err)
		}
		sandboxes = append(sandboxes, sandbox)
	}
	return sandboxes, nil
}

func writeDNSFiles(server DNSResourceServer, files []dns.File) error {
	for _, directory := range []string{"/etc/coredns", "/etc/coredns/zones"} {
		err := server.CreateInstanceFile("sc-dns", directory, incus.InstanceFileArgs{Type: "directory", Mode: 0o755})
		if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
			return fmt.Errorf("create DNS config directory %s: %w", directory, err)
		}
	}
	for _, file := range files {
		if err := server.CreateInstanceFile("sc-dns", file.Path, incus.InstanceFileArgs{
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
