package cli

import (
	"context"
	"strings"
	"testing"

	authapp "github.com/thieso2/sandcastle-incus/internal/authapp"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
)

// fakeRouteClient captures the request the CLI builds and returns scripted views.
type fakeRouteClient struct {
	published authapp.RoutePublishRequest
	view      authapp.RouteView
	listed    []authapp.RouteView
	deleted   string
}

func (f *fakeRouteClient) PublishRoute(_ context.Context, req authapp.RoutePublishRequest) (authapp.RouteView, error) {
	f.published = req
	if f.view.Hostname == "" {
		f.view = authapp.RouteView{Hostname: req.Machine + "." + req.Tenant + ".sc2.dev", URL: "https://" + req.Machine + "." + req.Tenant + ".sc2.dev", Machine: req.Machine, BackendPort: req.BackendPort, Status: "live"}
	}
	return f.view, nil
}
func (f *fakeRouteClient) ListRoutes(context.Context, string) ([]authapp.RouteView, error) {
	return f.listed, nil
}
func (f *fakeRouteClient) GetRouteStatus(context.Context, string) (authapp.RouteView, error) {
	return f.view, nil
}
func (f *fakeRouteClient) DeleteRoute(_ context.Context, hostname string) error {
	f.deleted = hostname
	return nil
}

// DeviceClient must satisfy the CLI's route client interface.
var _ authRouteClient = authapp.DeviceClient{}

func TestRoutePublishBuildsRequestFromArgs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeRouteClient{}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		adminConfig: scconfig.Admin{Tenant: "acme", Project: "web", Remote: "sc-acme", AuthToken: "x"},
		tenantStore: infoV2ProjectStore("sc2-acme-web"),
		authRoutes:  fake,
	}, "route", "publish", "web:api", "--port", "3000", "--name", "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if fake.published.Tenant != "acme" {
		t.Errorf("tenant = %q, want acme (from summary, not the arg)", fake.published.Tenant)
	}
	if fake.published.Project != "web" || fake.published.Machine != "api" {
		t.Errorf("project/machine = %q/%q, want web/api", fake.published.Project, fake.published.Machine)
	}
	if fake.published.BackendPort != 3000 {
		t.Errorf("port = %d, want 3000", fake.published.BackendPort)
	}
	if fake.published.Name != "myapp" {
		t.Errorf("name = %q, want myapp", fake.published.Name)
	}
	if !strings.Contains(stdout, "Published") {
		t.Errorf("stdout missing published line:\n%s", stdout)
	}
}

func TestRoutePublishRequiresPort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := executeForTestWithConfig(t, commandConfig{
		adminConfig: scconfig.Admin{Tenant: "acme", Project: "web", Remote: "sc-acme", AuthToken: "x"},
		authRoutes:  &fakeRouteClient{},
	}, "route", "publish", "web:api")
	if err == nil || !strings.Contains(err.Error(), "--port is required") {
		t.Fatalf("expected --port required error, got %v", err)
	}
}

func TestRoutePublishDryRunDoesNotCallClient(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeRouteClient{}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		adminConfig: scconfig.Admin{Tenant: "acme", Project: "web", Remote: "sc-acme", AuthToken: "x"},
		tenantStore: infoV2ProjectStore("sc2-acme-web"),
		authRoutes:  fake,
	}, "route", "publish", "web:api", "--port", "3000", "--name", "myapp", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if fake.published.Machine != "" {
		t.Fatalf("dry-run must not call the client, but it published %+v", fake.published)
	}
	if !strings.Contains(stdout, "dry run") || !strings.Contains(stdout, "myapp.acme") {
		t.Fatalf("dry-run output missing plan/hostname:\n%s", stdout)
	}
}

func TestRoutePublishCustomHostname(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &fakeRouteClient{}
	_, err := executeForTestWithConfig(t, commandConfig{
		adminConfig: scconfig.Admin{Tenant: "acme", Project: "web", Remote: "sc-acme", AuthToken: "x"},
		tenantStore: infoV2ProjectStore("sc2-acme-web"),
		authRoutes:  fake,
	}, "route", "publish", "web:api", "--port", "3000", "--hostname", "app.customer.com")
	if err != nil {
		t.Fatal(err)
	}
	if fake.published.Hostname != "app.customer.com" {
		t.Errorf("hostname = %q, want app.customer.com", fake.published.Hostname)
	}
}

func TestRouteListFormatsTable(t *testing.T) {
	out := formatRouteList([]authapp.RouteView{
		{Hostname: "web.acme.sc2.dev", Machine: "web", BackendPort: 3000, Status: "live"},
	})
	for _, want := range []string{"HOSTNAME", "web.acme.sc2.dev", "3000", "live"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}
	if empty := formatRouteList(nil); !strings.Contains(empty, "No published routes") {
		t.Errorf("empty list = %q", empty)
	}
}

func TestRouteStatusFormat(t *testing.T) {
	out := formatRouteStatus(authapp.RouteView{Hostname: "web.acme.sc2.dev", URL: "https://web.acme.sc2.dev", Tenant: "acme", Machine: "web", BackendPort: 3000, Status: "live"})
	for _, want := range []string{"web.acme.sc2.dev", "acme:web:3000", "live"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
}
