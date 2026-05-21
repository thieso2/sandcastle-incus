package incusx

import (
	"context"
	"io"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	"github.com/thieso2/sandcastle-incus/internal/meta"
)

type fakeDNSServer struct {
	resource *fakeDNSResource
}

func (s fakeDNSServer) UseProject(name string) DNSResourceServer {
	return s.resource
}

type fakeDNSResource struct {
	instances     []api.Instance
	files         map[string]string
	execInstances []string
	execCommands  [][]string
}

func (r *fakeDNSResource) GetInstances(instanceType api.InstanceType) ([]api.Instance, error) {
	return r.instances, nil
}

func (r *fakeDNSResource) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
	if r.files == nil {
		r.files = map[string]string{}
	}
	if args.Type == "directory" {
		r.files[path] = "<dir>"
		return nil
	}
	content, err := io.ReadAll(args.Content)
	if err != nil {
		return err
	}
	r.files[path] = string(content)
	return nil
}

func (r *fakeDNSResource) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	r.execInstances = append(r.execInstances, instanceName)
	r.execCommands = append(r.execCommands, exec.Command)
	if args.DataDone != nil {
		close(args.DataDone)
	}
	return fakeOperation{}, nil
}

func TestDNSManagerApply(t *testing.T) {
	machineConfig, err := meta.MachineConfig(meta.Machine{
		Tenant:    "acme",
		Project:   "default",
		Name:      "codex",
		AppPort:   3000,
		PrivateIP: "10.248.0.20",
	})
	if err != nil {
		t.Fatal(err)
	}
	resource := &fakeDNSResource{instances: []api.Instance{{
		Name:        "default-codex",
		InstancePut: api.InstancePut{Config: api.ConfigMap(machineConfig)},
	}}}
	manager := DNSManager{Server: fakeDNSServer{resource: resource}}
	result, err := manager.Apply(context.Background(), dns.Tenant{
		IncusName:   "sc-acme",
		Tenant:      "acme",
		DNSSuffix:   "acme",
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RecordCount != 4 {
		t.Fatalf("RecordCount = %d", result.RecordCount)
	}
	if resource.files["/etc/coredns/zones/db.acme"] == "" {
		t.Fatal("expected zone file to be written")
	}
	if len(resource.execCommands) != 1 {
		t.Fatalf("exec commands = %#v", resource.execCommands)
	}
	if resource.execInstances[0] != "sc-dns" {
		t.Fatalf("exec instance = %q", resource.execInstances[0])
	}
	if got := strings.Join(resource.execCommands[0], " "); !strings.Contains(got, "coredns -conf /etc/coredns/Corefile") {
		t.Fatalf("exec command = %q", got)
	}
}
