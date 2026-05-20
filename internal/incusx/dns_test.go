package incusx

import (
	"context"
	"io"
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
	instances []api.Instance
	files     map[string]string
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

func TestDNSManagerApply(t *testing.T) {
	sandboxConfig, err := meta.SandboxConfig(meta.Sandbox{
		Owner:     "alice",
		Project:   "myproject",
		Name:      "codex",
		AppPort:   3000,
		PrivateIP: "10.248.0.20",
	})
	if err != nil {
		t.Fatal(err)
	}
	resource := &fakeDNSResource{instances: []api.Instance{{
		Name:        "sc-codex",
		InstancePut: api.InstancePut{Config: api.ConfigMap(sandboxConfig)},
	}}}
	manager := DNSManager{Server: fakeDNSServer{resource: resource}}
	result, err := manager.Apply(context.Background(), dns.Project{
		IncusName:   "sc-alice-myproject",
		Owner:       "alice",
		Name:        "myproject",
		Domain:      "myproject.project-tld",
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RecordCount != 4 {
		t.Fatalf("RecordCount = %d", result.RecordCount)
	}
	if resource.files["/etc/coredns/zones/db.myproject.project-tld"] == "" {
		t.Fatal("expected zone file to be written")
	}
}
