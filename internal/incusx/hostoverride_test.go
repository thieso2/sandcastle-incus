package incusx

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/certs"
	"github.com/thieso2/sandcastle-incus/internal/hostoverride"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type fakeHostOverrideServer struct {
	resource *fakeHostOverrideResource
}

func (s fakeHostOverrideServer) UseProject(name string) HostOverrideResourceServer {
	return s.resource
}

type fakeHostOverrideResource struct {
	instances    []api.Instance
	instance     *api.Instance
	updated      *api.InstancePut
	createdFiles map[string]string
	caFiles      map[string]string
	execCommands [][]string
}

func (r *fakeHostOverrideResource) GetInstances(instanceType api.InstanceType) ([]api.Instance, error) {
	return r.instances, nil
}

func (r *fakeHostOverrideResource) GetInstance(name string) (*api.Instance, string, error) {
	return r.instance, "etag", nil
}

func (r *fakeHostOverrideResource) UpdateInstance(name string, instance api.InstancePut, etag string) (incus.Operation, error) {
	r.updated = &instance
	return fakeOperation{}, nil
}

func (r *fakeHostOverrideResource) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
	if r.createdFiles == nil {
		r.createdFiles = map[string]string{}
	}
	if args.Content == nil {
		r.createdFiles[path] = args.Type
		return nil
	}
	content, err := io.ReadAll(args.Content)
	if err != nil {
		return err
	}
	r.createdFiles[path] = string(content)
	return nil
}

func (r *fakeHostOverrideResource) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	content := r.caFiles[filePath]
	return io.NopCloser(strings.NewReader(content)), &incus.InstanceFileResponse{Type: "file"}, nil
}

func (r *fakeHostOverrideResource) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	r.execCommands = append(r.execCommands, exec.Command)
	if args.DataDone != nil {
		close(args.DataDone)
	}
	return fakeOperation{}, nil
}

func TestHostOverrideManagerFindsSandbox(t *testing.T) {
	configMap, err := meta.SandboxConfig(meta.Sandbox{
		Owner:     "alice",
		Project:   "myproject",
		Name:      "codex",
		AppPort:   3000,
		PrivateIP: "10.248.0.20",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := HostOverrideManager{Server: fakeHostOverrideServer{resource: &fakeHostOverrideResource{
		instances: []api.Instance{{Name: "sc-codex", InstancePut: api.InstancePut{Config: api.ConfigMap(configMap)}}},
	}}}
	sandbox, err := manager.FindSandbox(context.Background(), project.Summary{
		IncusName: "sc-alice-myproject",
		Owner:     "alice",
		Name:      "myproject",
	}, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if sandbox.PrivateIP != "10.248.0.20" {
		t.Fatalf("PrivateIP = %q", sandbox.PrivateIP)
	}
}

func TestHostOverrideManagerAddUpdatesMetadataAndWritesFiles(t *testing.T) {
	configMap, err := meta.SandboxConfig(meta.Sandbox{
		Owner:     "alice",
		Project:   "myproject",
		Name:      "codex",
		AppPort:   3000,
		PrivateIP: "10.248.0.20",
	})
	if err != nil {
		t.Fatal(err)
	}
	ca, err := certs.GenerateCA("test CA", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	resource := &fakeHostOverrideResource{
		instance: &api.Instance{Name: "sc-codex", InstancePut: api.InstancePut{Config: api.ConfigMap(configMap)}},
		caFiles: map[string]string{
			project.ProjectCACertPath: string(ca.CertificatePEM),
			project.ProjectCAKeyPath:  string(ca.PrivateKeyPEM),
		},
	}
	manager := HostOverrideManager{Server: fakeHostOverrideServer{resource: resource}}
	err = manager.Add(context.Background(), hostoverride.AddPlan{
		Reference:    "alice/myproject/codex",
		Project:      project.Summary{IncusName: "sc-alice-myproject", Owner: "alice", Name: "myproject", Domain: "myproject.project-tld"},
		Sandbox:      meta.Sandbox{Name: "codex", AppPort: 3000, PrivateIP: "10.248.0.20"},
		InstanceName: "sc-codex",
		StoragePool:  "default",
		CAVolume:     project.CAVolumeName,
		Hostname:     "example.com",
		ExtraSANs:    []string{"example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resource.updated == nil {
		t.Fatal("expected instance metadata update")
	}
	updated, err := meta.ParseSandboxConfig(map[string]string(resource.updated.Config))
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.ExtraSANs) != 1 || updated.ExtraSANs[0] != "example.com" {
		t.Fatalf("ExtraSANs = %#v", updated.ExtraSANs)
	}
	if !strings.Contains(resource.createdFiles["/etc/caddy/Caddyfile"], "example.com") {
		t.Fatalf("Caddyfile = %q", resource.createdFiles["/etc/caddy/Caddyfile"])
	}
	if resource.createdFiles["/etc/caddy/certs/tls.crt"] == "" {
		t.Fatal("expected certificate write")
	}
	if len(resource.execCommands) != 1 || !strings.Contains(strings.Join(resource.execCommands[0], " "), "caddy") {
		t.Fatalf("exec commands = %#v", resource.execCommands)
	}
}

func TestHostOverrideManagerRemoveUpdatesMetadataAndWritesFiles(t *testing.T) {
	configMap, err := meta.SandboxConfig(meta.Sandbox{
		Owner:     "alice",
		Project:   "myproject",
		Name:      "codex",
		AppPort:   3000,
		PrivateIP: "10.248.0.20",
		ExtraSANs: []string{"example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ca, err := certs.GenerateCA("test CA", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	resource := &fakeHostOverrideResource{
		instance: &api.Instance{Name: "sc-codex", InstancePut: api.InstancePut{Config: api.ConfigMap(configMap)}},
		caFiles: map[string]string{
			project.ProjectCACertPath: string(ca.CertificatePEM),
			project.ProjectCAKeyPath:  string(ca.PrivateKeyPEM),
		},
	}
	manager := HostOverrideManager{Server: fakeHostOverrideServer{resource: resource}}
	err = manager.Remove(context.Background(), hostoverride.RemovePlan{
		Reference:    "alice/myproject/codex",
		Project:      project.Summary{IncusName: "sc-alice-myproject", Owner: "alice", Name: "myproject", Domain: "myproject.project-tld"},
		Sandbox:      meta.Sandbox{Name: "codex", AppPort: 3000, PrivateIP: "10.248.0.20", ExtraSANs: []string{"example.com"}},
		InstanceName: "sc-codex",
		StoragePool:  "default",
		CAVolume:     project.CAVolumeName,
		Hostname:     "example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := meta.ParseSandboxConfig(map[string]string(resource.updated.Config))
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.ExtraSANs) != 0 {
		t.Fatalf("ExtraSANs = %#v", updated.ExtraSANs)
	}
	if strings.Contains(resource.createdFiles["/etc/caddy/Caddyfile"], "example.com") {
		t.Fatalf("Caddyfile = %q", resource.createdFiles["/etc/caddy/Caddyfile"])
	}
}
