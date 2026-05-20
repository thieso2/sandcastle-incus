package incusx

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
)

type fakeTopologyServer struct {
	resource *fakeTopologyResource
}

func (s fakeTopologyServer) UseProject(name string) TopologyResourceServer {
	return s.resource
}

type fakeTopologyResource struct {
	networks  map[string]*api.Network
	volumes   map[string]*api.StorageVolume
	instances map[string]*api.Instance
	files     map[string]string
}

func (r fakeTopologyResource) GetNetwork(name string) (*api.Network, string, error) {
	if network := r.networks[name]; network != nil {
		return network, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (r fakeTopologyResource) GetStoragePoolVolume(pool string, volType string, name string) (*api.StorageVolume, string, error) {
	if volume := r.volumes[name]; volume != nil {
		return volume, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (r fakeTopologyResource) GetInstance(name string) (*api.Instance, string, error) {
	if instance := r.instances[name]; instance != nil {
		return instance, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (r fakeTopologyResource) GetInstances(instanceType api.InstanceType) ([]api.Instance, error) {
	instances := make([]api.Instance, 0, len(r.instances))
	for _, instance := range r.instances {
		instances = append(instances, *instance)
	}
	return instances, nil
}

func (r fakeTopologyResource) GetInstanceFile(instanceName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	if content, ok := r.files[instanceName+":"+filePath]; ok {
		return io.NopCloser(strings.NewReader(content)), &incus.InstanceFileResponse{Type: "file"}, nil
	}
	return nil, nil, api.StatusErrorf(http.StatusNotFound, "not found")
}

func TestTopologyStoreGetTopology(t *testing.T) {
	store := TopologyStore{Server: fakeTopologyServer{resource: &fakeTopologyResource{
		networks: map[string]*api.Network{project.PrivateNetworkName: {Name: project.PrivateNetworkName}},
		volumes: map[string]*api.StorageVolume{
			project.HomeVolumeName: {Name: project.HomeVolumeName},
			project.CAVolumeName:   {Name: project.CAVolumeName},
		},
		instances: map[string]*api.Instance{
			project.TailscaleName: {Name: project.TailscaleName, Status: "Stopped", StatusCode: api.Stopped},
			project.DNSName:       {Name: project.DNSName, Status: "Running", StatusCode: api.Running},
			"sc-codex": {
				Name: "sc-codex",
				InstancePut: api.InstancePut{
					Config: map[string]string{
						meta.KeyKind:    meta.KindSandbox,
						meta.KeyVersion: "1",
					},
				},
			},
			"manual": {
				Name: "manual",
				InstancePut: api.InstancePut{
					Config: map[string]string{
						meta.KeyKind: "manual",
					},
				},
			},
		},
		files: map[string]string{
			project.DNSName + ":/etc/coredns/Corefile":                       ".:53 {\n  errors\n}\n",
			project.DNSName + ":/etc/coredns/zones/db.myproject.project-tld": "$ORIGIN myproject.project-tld.\n",
			"sc-codex:" + sandbox.CaddyfilePath:                              "codex.myproject.project-tld {\n  reverse_proxy localhost:3000\n}\n",
		},
	}}}
	topology, err := store.GetTopology(context.Background(), project.TopologyRequest{
		IncusProject: "sc-alice-myproject",
		StoragePool:  "default",
		Domain:       "myproject.project-tld",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !topology.PrivateNetworkPresent {
		t.Fatal("private network should be present")
	}
	if !topology.DurableVolumes[project.HomeVolumeName] {
		t.Fatal("home volume should be present")
	}
	if topology.DurableVolumes[project.WorkspaceVolumeName] {
		t.Fatal("workspace volume should be missing")
	}
	if topology.Sidecars[project.TailscaleName].Running {
		t.Fatal("tailscale sidecar should be stopped")
	}
	if !topology.Sidecars[project.DNSName].Running {
		t.Fatal("dns sidecar should be running")
	}
	if len(topology.DiagnosticFiles) != 3 {
		t.Fatalf("DiagnosticFiles = %#v, want CoreDNS files and sandbox Caddyfile", topology.DiagnosticFiles)
	}
	if topology.DiagnosticFiles[0].Path != "/etc/coredns/Corefile" || !strings.Contains(topology.DiagnosticFiles[0].Content, "errors") {
		t.Fatalf("Corefile diagnostic = %#v", topology.DiagnosticFiles[0])
	}
	if topology.DiagnosticFiles[1].Path != "/etc/coredns/zones/db.myproject.project-tld" || !strings.Contains(topology.DiagnosticFiles[1].Content, "$ORIGIN") {
		t.Fatalf("zone diagnostic = %#v", topology.DiagnosticFiles[1])
	}
	if topology.DiagnosticFiles[2].Instance != "sc-codex" || topology.DiagnosticFiles[2].Path != sandbox.CaddyfilePath || !strings.Contains(topology.DiagnosticFiles[2].Content, "reverse_proxy") {
		t.Fatalf("sandbox Caddyfile diagnostic = %#v", topology.DiagnosticFiles[2])
	}
}
