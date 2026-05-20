package incusx

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/tailscale"
)

type fakeTailscaleServer struct {
	resource *fakeTailscaleResource
	project  *api.Project
	updated  *api.ProjectPut
}

func (s *fakeTailscaleServer) UseProject(name string) TailscaleResourceServer {
	return s.resource
}

func (s *fakeTailscaleServer) GetProject(name string) (*api.Project, string, error) {
	return s.project, "etag", nil
}

func (s *fakeTailscaleServer) UpdateProject(name string, project api.ProjectPut, etag string) error {
	s.updated = &project
	return nil
}

type fakeTailscaleResource struct {
	instanceName string
	exec         api.InstanceExecPost
	stdout       string
}

func (r *fakeTailscaleResource) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	r.instanceName = instanceName
	r.exec = exec
	if args.Stdout != nil && r.stdout != "" {
		_, _ = args.Stdout.Write([]byte(r.stdout))
	}
	if args.DataDone != nil {
		close(args.DataDone)
	}
	return fakeOperation{}, nil
}

func TestTailscaleManagerRunsUpInSidecar(t *testing.T) {
	resource := &fakeTailscaleResource{}
	manager := TailscaleManager{Server: &fakeTailscaleServer{resource: resource}}
	err := manager.RunUp(context.Background(), tailscale.UpPlan{
		Project:         project.Summary{IncusName: "sc-alice-myproject"},
		InstanceName:    project.TailscaleName,
		AdvertiseRoutes: []string{"10.248.0.0/24"},
		AdvertiseTags:   []string{"tag:sandcastle"},
		AuthKey:         "tskey-secret",
	}, tailscale.RunSession{Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if resource.instanceName != project.TailscaleName {
		t.Fatalf("instanceName = %q", resource.instanceName)
	}
	command := strings.Join(resource.exec.Command, " ")
	if !strings.Contains(command, "--advertise-routes=10.248.0.0/24") {
		t.Fatalf("command = %q", command)
	}
	if !strings.Contains(command, "--advertise-tags=tag:sandcastle") {
		t.Fatalf("command = %q", command)
	}
	if !strings.Contains(command, "--auth-key=tskey-secret") {
		t.Fatalf("command = %q", command)
	}
}

func TestTailscaleManagerRunsStatusAndUpdatesMetadata(t *testing.T) {
	configMap := projectConfigForTailscaleTest(t)
	resource := &fakeTailscaleResource{stdout: `{
		"BackendState": "Running",
		"AuthURL": "https://login.tailscale.com/a/secret-token",
		"CurrentTailnet": {"Name": "example.com"},
		"Self": {
			"HostName": "sc-myproject",
			"LoginURL": "https://login.tailscale.com/b/secret-token",
			"TailscaleIPs": ["100.80.12.34"],
			"PrimaryRoutes": ["10.248.0.0/24"]
		}
	}`}
	server := &fakeTailscaleServer{
		resource: resource,
		project:  &api.Project{Name: "sc-alice-myproject", ProjectPut: api.ProjectPut{Config: api.ConfigMap(configMap)}},
	}
	manager := TailscaleManager{Server: server}
	result, err := manager.RunStatus(context.Background(), tailscale.StatusPlan{
		Reference:    "alice/myproject",
		Project:      project.Summary{IncusName: "sc-alice-myproject", Owner: "alice", Name: "myproject"},
		InstanceName: project.TailscaleName,
		Command:      []string{"tailscale", "status", "--json"},
	}, tailscale.RunSession{Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if result.Tailscale.State != "Running" {
		t.Fatalf("State = %q", result.Tailscale.State)
	}
	if server.updated == nil {
		t.Fatal("expected project metadata update")
	}
	parsed, err := meta.ParseProjectConfig(map[string]string(server.updated.Config))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Tailscale.Tailnet != "example.com" {
		t.Fatalf("Tailnet = %q", parsed.Tailscale.Tailnet)
	}
	encoded, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"login.tailscale.com", "secret-token"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("project metadata leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestTailscaleManagerRunsDownAndUpdatesMetadata(t *testing.T) {
	configMap := projectConfigForTailscaleTest(t)
	server := &fakeTailscaleServer{
		resource: &fakeTailscaleResource{},
		project:  &api.Project{Name: "sc-alice-myproject", ProjectPut: api.ProjectPut{Config: api.ConfigMap(configMap)}},
	}
	manager := TailscaleManager{Server: server}
	err := manager.RunDown(context.Background(), tailscale.DownPlan{
		Project:      project.Summary{IncusName: "sc-alice-myproject"},
		InstanceName: project.TailscaleName,
		Command:      []string{"tailscale", "down"},
	}, tailscale.RunSession{Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if server.updated == nil {
		t.Fatal("expected project metadata update")
	}
	parsed, err := meta.ParseProjectConfig(map[string]string(server.updated.Config))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Tailscale.State != "stopped" {
		t.Fatalf("State = %q", parsed.Tailscale.State)
	}
}

func projectConfigForTailscaleTest(t *testing.T) map[string]string {
	t.Helper()
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	return configMap
}
