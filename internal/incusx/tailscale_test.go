package incusx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/tailscale"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type fakeTailscaleServer struct {
	resource  *fakeTailscaleResource
	project   *api.Project
	updated   *api.ProjectPut
	updateErr error
}

func (s *fakeTailscaleServer) UseProject(name string) TailscaleResourceServer {
	return s.resource
}

func (s *fakeTailscaleServer) GetProject(name string) (*api.Project, string, error) {
	return s.project, "etag", nil
}

func (s *fakeTailscaleServer) UpdateProject(name string, project api.ProjectPut, etag string) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.updated = &project
	return nil
}

type fakeTailscaleResource struct {
	instanceName string
	exec         api.InstanceExecPost
	execs        []api.InstanceExecPost
	stdout       string
	stdouts      []string
}

func (r *fakeTailscaleResource) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	r.instanceName = instanceName
	if len(r.execs) == 0 {
		r.exec = exec
	}
	r.execs = append(r.execs, exec)
	stdout := r.stdout
	if len(r.stdouts) > 0 {
		stdout = r.stdouts[0]
		r.stdouts = r.stdouts[1:]
	}
	if args.Stdout != nil && stdout != "" {
		_, _ = args.Stdout.Write([]byte(stdout))
	}
	if args.DataDone != nil {
		close(args.DataDone)
	}
	return fakeOperation{}, nil
}

func TestTailscaleManagerRunsUpInSidecar(t *testing.T) {
	configMap := projectConfigForTailscaleTest(t)
	resource := &fakeTailscaleResource{stdouts: []string{"", `{
		"BackendState": "Running",
		"CurrentTailnet": {"Name": "example.com"},
		"Self": {
			"HostName": "sc-acme",
			"TailscaleIPs": ["100.80.12.34"],
			"PrimaryRoutes": ["10.248.0.0/24"]
		}
	}`}}
	server := &fakeTailscaleServer{
		resource: resource,
		project:  &api.Project{Name: "sc-acme", ProjectPut: api.ProjectPut{Config: api.ConfigMap(configMap)}},
	}
	manager := TailscaleManager{Server: server}
	err := manager.RunUp(context.Background(), tailscale.UpPlan{
		Reference:       "acme",
		Tenant:          tenant.Summary{IncusName: "sc-acme"},
		InstanceName:    "sc-acme",
		AdvertiseRoutes: []string{"10.248.0.0/24"},
		AdvertiseTags:   []string{"tag:sandcastle"},
		AuthKey:         "tskey-secret",
	}, tailscale.RunSession{Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if resource.instanceName != "sc-acme" {
		t.Fatalf("instanceName = %q", resource.instanceName)
	}
	if len(resource.execs) != 2 {
		t.Fatalf("exec count = %d, want 2", len(resource.execs))
	}
	command := strings.Join(resource.execs[0].Command, " ")
	if !strings.Contains(command, "--advertise-routes=10.248.0.0/24") {
		t.Fatalf("command = %q", command)
	}
	if !strings.Contains(command, "--advertise-tags=tag:sandcastle") {
		t.Fatalf("command = %q", command)
	}
	if !strings.Contains(command, "--auth-key=tskey-secret") {
		t.Fatalf("command = %q", command)
	}
	statusCommand := strings.Join(resource.execs[1].Command, " ")
	if statusCommand != "tailscale status --json" {
		t.Fatalf("status command = %q", statusCommand)
	}
	if server.updated == nil {
		t.Fatal("expected tenant metadata update")
	}
}

func TestTailscaleManagerRunUpRejectsUnauthenticatedSidecar(t *testing.T) {
	configMap := projectConfigForTailscaleTest(t)
	resource := &fakeTailscaleResource{stdouts: []string{"", `{
		"BackendState": "NeedsLogin",
		"Self": {"HostName": "sc-acme"}
	}`}}
	server := &fakeTailscaleServer{
		resource: resource,
		project:  &api.Project{Name: "sc-acme", ProjectPut: api.ProjectPut{Config: api.ConfigMap(configMap)}},
	}
	manager := TailscaleManager{Server: server}
	err := manager.RunUp(context.Background(), tailscale.UpPlan{
		Reference:       "acme",
		Tenant:          tenant.Summary{IncusName: "sc-acme"},
		InstanceName:    "sc-acme",
		AdvertiseRoutes: []string{"10.248.0.0/24"},
		AdvertiseTags:   []string{"tag:sandcastle"},
		AuthKey:         "tskey-secret",
	}, tailscale.RunSession{Stdout: io.Discard, Stderr: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "did not authenticate sidecar") {
		t.Fatalf("error = %v, want unauthenticated sidecar", err)
	}
}

func TestTailscaleManagerRunUpStillValidatesWhenStatusMetadataUpdateIsRestricted(t *testing.T) {
	configMap := projectConfigForTailscaleTest(t)
	resource := &fakeTailscaleResource{stdouts: []string{"", `{
		"BackendState": "NeedsLogin",
		"Self": {"HostName": "sc-acme"}
	}`}}
	server := &fakeTailscaleServer{
		resource:  resource,
		project:   &api.Project{Name: "sc-acme", ProjectPut: api.ProjectPut{Config: api.ConfigMap(configMap)}},
		updateErr: fmt.Errorf("Certificate is restricted"),
	}
	manager := TailscaleManager{Server: server}
	err := manager.RunUp(context.Background(), tailscale.UpPlan{
		Reference:       "acme",
		Tenant:          tenant.Summary{IncusName: "sc-acme"},
		InstanceName:    "sc-acme",
		AdvertiseRoutes: []string{"10.248.0.0/24"},
		AdvertiseTags:   []string{"tag:sandcastle"},
		AuthKey:         "tskey-secret",
	}, tailscale.RunSession{Stdout: io.Discard, Stderr: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "did not authenticate sidecar") {
		t.Fatalf("error = %v, want unauthenticated sidecar", err)
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
		project:  &api.Project{Name: "sc-acme", ProjectPut: api.ProjectPut{Config: api.ConfigMap(configMap)}},
	}
	manager := TailscaleManager{Server: server}
	result, err := manager.RunStatus(context.Background(), tailscale.StatusPlan{
		Reference:    "acme",
		Tenant:       tenant.Summary{IncusName: "sc-acme", Tenant: "acme"},
		InstanceName: "sc-acme",
		Command:      []string{"tailscale", "status", "--json"},
	}, tailscale.RunSession{Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if result.Tailscale.State != "Running" {
		t.Fatalf("State = %q", result.Tailscale.State)
	}
	if server.updated == nil {
		t.Fatal("expected tenant metadata update")
	}
	parsed, err := meta.ParseTenantConfig(map[string]string(server.updated.Config))
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
			t.Fatalf("tenant metadata leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestTailscaleManagerRunsDownAndUpdatesMetadata(t *testing.T) {
	configMap := projectConfigForTailscaleTest(t)
	server := &fakeTailscaleServer{
		resource: &fakeTailscaleResource{},
		project:  &api.Project{Name: "sc-acme", ProjectPut: api.ProjectPut{Config: api.ConfigMap(configMap)}},
	}
	manager := TailscaleManager{Server: server}
	err := manager.RunDown(context.Background(), tailscale.DownPlan{
		Tenant:       tenant.Summary{IncusName: "sc-acme"},
		InstanceName: "sc-acme",
		Command:      []string{"tailscale", "down"},
	}, tailscale.RunSession{Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if server.updated == nil {
		t.Fatal("expected tenant metadata update")
	}
	parsed, err := meta.ParseTenantConfig(map[string]string(server.updated.Config))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Tailscale.State != "stopped" {
		t.Fatalf("State = %q", parsed.Tailscale.State)
	}
}

func projectConfigForTailscaleTest(t *testing.T) map[string]string {
	t.Helper()
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	return configMap
}
