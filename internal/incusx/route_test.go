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
	"github.com/thieso2/sandcastle-incus/internal/route"
)

type fakeRouteServer struct {
	resource        *fakeRouteResourceServer
	targetResource  *fakeRouteResourceServer
	project         string
	targetProject   string
	infrastructure  string
	projectMetadata *api.Project
	updatedProject  *api.ProjectPut
}

func (s *fakeRouteServer) UseProject(name string) RouteResourceServer {
	if s.infrastructure != "" && name != s.infrastructure {
		s.targetProject = name
		if s.targetResource != nil {
			return s.targetResource
		}
	}
	s.project = name
	return s.resource
}

func (s *fakeRouteServer) GetProject(name string) (*api.Project, string, error) {
	if s.projectMetadata != nil {
		return s.projectMetadata, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (s *fakeRouteServer) UpdateProject(name string, project api.ProjectPut, etag string) error {
	s.updatedProject = &project
	s.projectMetadata = &api.Project{Name: name, ProjectPut: project}
	return nil
}

type fakeRouteResourceServer struct {
	profiles       map[string]*api.Profile
	createdProfile *api.ProfilesPost
	updatedProfile *api.ProfilePut
	deletedProfile string
	createdFiles   map[string]string
	execInstance   string
	exec           api.InstanceExecPost
	instance       *api.Instance
	updated        *api.InstancePut
}

func (s *fakeRouteResourceServer) GetInstance(name string) (*api.Instance, string, error) {
	if s.instance != nil {
		return s.instance, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (s *fakeRouteResourceServer) UpdateInstance(name string, instance api.InstancePut, etag string) (incus.Operation, error) {
	s.updated = &instance
	s.instance = &api.Instance{Name: name, InstancePut: instance}
	return fakeOperation{}, nil
}

func (s *fakeRouteResourceServer) GetProfile(name string) (*api.Profile, string, error) {
	if profile := s.profiles[name]; profile != nil {
		return profile, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (s *fakeRouteResourceServer) GetProfiles() ([]api.Profile, error) {
	profiles := []api.Profile{}
	for _, profile := range s.profiles {
		profiles = append(profiles, *profile)
	}
	return profiles, nil
}

func (s *fakeRouteResourceServer) CreateProfile(profile api.ProfilesPost) error {
	s.createdProfile = &profile
	s.profiles[profile.Name] = &api.Profile{Name: profile.Name, ProfilePut: profile.ProfilePut}
	return nil
}

func (s *fakeRouteResourceServer) UpdateProfile(name string, profile api.ProfilePut, etag string) error {
	s.updatedProfile = &profile
	s.profiles[name] = &api.Profile{Name: name, ProfilePut: profile}
	return nil
}

func (s *fakeRouteResourceServer) DeleteProfile(name string) error {
	s.deletedProfile = name
	delete(s.profiles, name)
	return nil
}

func (s *fakeRouteResourceServer) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
	if s.createdFiles == nil {
		s.createdFiles = map[string]string{}
	}
	if args.Content != nil {
		content, _ := io.ReadAll(args.Content)
		s.createdFiles[instanceName+":"+path] = string(content)
	} else {
		s.createdFiles[instanceName+":"+path] = args.Type
	}
	return nil
}

func (s *fakeRouteResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	s.execInstance = instanceName
	s.exec = exec
	if args.DataDone != nil {
		close(args.DataDone)
	}
	return fakeOperation{}, nil
}

func TestRouteManagerCreatesRouteProfile(t *testing.T) {
	resource := &fakeRouteResourceServer{profiles: map[string]*api.Profile{}}
	target := &fakeRouteResourceServer{instance: &api.Instance{Name: "sc-codex", InstancePut: api.InstancePut{Devices: api.DevicesMap{}}}}
	server := &fakeRouteServer{
		resource:        resource,
		targetResource:  target,
		infrastructure:  "sc-infra",
		projectMetadata: projectMetadataForRouteTest(t, nil),
	}
	manager := RouteManager{
		Server:           server,
		Resolver:         fakeRouteDNSResolver{hosts: []string{"203.0.113.10"}},
		LetsEncryptEmail: "ops@example.com",
	}
	plan := routePlanForTest(t)
	if err := manager.Add(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if resource.createdProfile == nil {
		t.Fatal("expected profile creation")
	}
	if resource.createdProfile.Name != "sc-route-app-example-com" {
		t.Fatalf("created profile = %q", resource.createdProfile.Name)
	}
	routeMetadata, err := meta.ParseRouteConfig(map[string]string(resource.createdProfile.Config))
	if err != nil {
		t.Fatal(err)
	}
	if routeMetadata.RoutePort != 5173 {
		t.Fatalf("RoutePort = %d", routeMetadata.RoutePort)
	}
	caddyfile := resource.createdFiles["sc-caddy:/etc/caddy/Caddyfile"]
	if !strings.Contains(caddyfile, "app.example.com") || !strings.Contains(caddyfile, "10.248.0.20:5173") {
		t.Fatalf("Caddyfile = %q", caddyfile)
	}
	if !strings.HasPrefix(caddyfile, "{\n    email ops@example.com\n}\n\n") {
		t.Fatalf("Caddyfile missing Let's Encrypt email: %q", caddyfile)
	}
	if resource.execInstance != route.InfrastructureCaddyName {
		t.Fatalf("reload instance = %q", resource.execInstance)
	}
	if got := strings.Join(resource.exec.Command, " "); got != "caddy reload --config /etc/caddy/Caddyfile" {
		t.Fatalf("reload command = %q", got)
	}
	if target.updated == nil {
		t.Fatal("expected target sandbox ingress device update")
	}
	if device := target.updated.Devices["sc-route-app-example-com"]; device["parent"] != "sc-private" || device["user.sandcastle.hostname"] != "app.example.com" {
		t.Fatalf("ingress device = %#v", device)
	}
	if server.updatedProject == nil {
		t.Fatal("expected target project route backlink update")
	}
	projectMetadata, err := meta.ParseProjectConfig(map[string]string(server.updatedProject.Config))
	if err != nil {
		t.Fatal(err)
	}
	if len(projectMetadata.PublicRoutes) != 1 {
		t.Fatalf("public routes = %#v", projectMetadata.PublicRoutes)
	}
	if projectMetadata.PublicRoutes[0].Hostname != "app.example.com" || projectMetadata.PublicRoutes[0].Sandbox != "codex" || projectMetadata.PublicRoutes[0].RoutePort != 5173 {
		t.Fatalf("public route backlink = %#v", projectMetadata.PublicRoutes[0])
	}
}

func TestRouteManagerRejectsRouteWhenDNSProofFails(t *testing.T) {
	resource := &fakeRouteResourceServer{profiles: map[string]*api.Profile{}}
	target := &fakeRouteResourceServer{instance: &api.Instance{Name: "sc-codex", InstancePut: api.InstancePut{Devices: api.DevicesMap{}}}}
	manager := RouteManager{
		Server:   &fakeRouteServer{resource: resource, targetResource: target, infrastructure: "sc-infra"},
		Resolver: fakeRouteDNSResolver{hosts: []string{"203.0.113.11"}},
	}
	if err := manager.Add(context.Background(), routePlanForTest(t)); err == nil {
		t.Fatal("expected DNS proof error")
	}
	if target.updated != nil {
		t.Fatal("target sandbox should not be mutated when DNS proof fails")
	}
	if resource.createdProfile != nil {
		t.Fatal("route metadata should not be created when DNS proof fails")
	}
}

func TestRouteManagerRejectsClaimedHostname(t *testing.T) {
	metadata, err := meta.RouteConfig(meta.Route{
		Hostname:      "app.example.com",
		TargetOwner:   "bob",
		TargetProject: "other",
		TargetSandbox: "web",
		TargetIP:      "10.248.0.30",
		RoutePort:     3000,
	})
	if err != nil {
		t.Fatal(err)
	}
	resource := &fakeRouteResourceServer{profiles: map[string]*api.Profile{
		"sc-route-app-example-com": {Name: "sc-route-app-example-com", ProfilePut: api.ProfilePut{Config: api.ConfigMap(metadata)}},
	}}
	target := &fakeRouteResourceServer{instance: &api.Instance{Name: "sc-codex", InstancePut: api.InstancePut{Devices: api.DevicesMap{}}}}
	manager := RouteManager{
		Server:   &fakeRouteServer{resource: resource, targetResource: target, infrastructure: "sc-infra"},
		Resolver: fakeRouteDNSResolver{hosts: []string{"203.0.113.10"}},
	}
	err = manager.Add(context.Background(), routePlanForTest(t))
	if err == nil {
		t.Fatal("expected claimed hostname error")
	}
	if !strings.Contains(err.Error(), "already claimed by bob/other/web") {
		t.Fatalf("error = %q", err.Error())
	}
	if target.updated != nil {
		t.Fatal("target sandbox should not be mutated when route hostname is claimed")
	}
	if resource.updatedProfile != nil {
		t.Fatal("existing route metadata should not be updated")
	}
}

func TestRouteManagerRejectsInfrastructureProfileConflict(t *testing.T) {
	resource := &fakeRouteResourceServer{profiles: map[string]*api.Profile{
		"sc-route-app-example-com": {Name: "sc-route-app-example-com", ProfilePut: api.ProfilePut{Config: api.ConfigMap{}}},
	}}
	target := &fakeRouteResourceServer{instance: &api.Instance{Name: "sc-codex", InstancePut: api.InstancePut{Devices: api.DevicesMap{}}}}
	manager := RouteManager{
		Server:   &fakeRouteServer{resource: resource, targetResource: target, infrastructure: "sc-infra"},
		Resolver: fakeRouteDNSResolver{hosts: []string{"203.0.113.10"}},
	}
	err := manager.Add(context.Background(), routePlanForTest(t))
	if err == nil {
		t.Fatal("expected profile conflict error")
	}
	if !strings.Contains(err.Error(), "conflicts with existing infrastructure profile") {
		t.Fatalf("error = %q", err.Error())
	}
	if target.updated != nil {
		t.Fatal("target sandbox should not be mutated when profile conflicts")
	}
}

func TestRouteManagerListsRouteProfiles(t *testing.T) {
	metadata, err := meta.RouteConfig(meta.Route{
		Hostname:      "app.example.com",
		TargetOwner:   "alice",
		TargetProject: "myproject",
		TargetSandbox: "codex",
		TargetIP:      "10.248.0.20",
		RoutePort:     5173,
	})
	if err != nil {
		t.Fatal(err)
	}
	resource := &fakeRouteResourceServer{profiles: map[string]*api.Profile{
		"sc-route-app-example-com": {Name: "sc-route-app-example-com", ProfilePut: api.ProfilePut{Config: api.ConfigMap(metadata)}},
		"default":                  {Name: "default", ProfilePut: api.ProfilePut{Config: api.ConfigMap{}}},
	}}
	manager := RouteManager{Server: &fakeRouteServer{resource: resource}}
	result, err := manager.List(context.Background(), route.ListPlan{InfrastructureProject: "sc-infra"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 1 {
		t.Fatalf("routes = %#v", result.Routes)
	}
	if result.Routes[0].TargetReference != "alice/myproject/codex" {
		t.Fatalf("TargetReference = %q", result.Routes[0].TargetReference)
	}
}

func TestRouteManagerFindsRouteMetadataByHostname(t *testing.T) {
	metadata, err := meta.RouteConfig(meta.Route{
		Hostname:      "app.example.com",
		TargetOwner:   "alice",
		TargetProject: "myproject",
		TargetSandbox: "codex",
		TargetIP:      "10.248.0.20",
		RoutePort:     5173,
	})
	if err != nil {
		t.Fatal(err)
	}
	resource := &fakeRouteResourceServer{profiles: map[string]*api.Profile{
		"sc-route-app-example-com": {Name: "sc-route-app-example-com", ProfilePut: api.ProfilePut{Config: api.ConfigMap(metadata)}},
	}}
	manager := RouteManager{
		Server:                &fakeRouteServer{resource: resource},
		InfrastructureProject: "sc-infra",
	}
	routeMetadata, err := manager.FindRoute(context.Background(), "app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if routeMetadata.TargetOwner != "alice" || routeMetadata.TargetSandbox != "codex" {
		t.Fatalf("routeMetadata = %#v", routeMetadata)
	}
}

func TestRouteManagerRemovesRouteProfile(t *testing.T) {
	metadata, err := meta.RouteConfig(meta.Route{
		Hostname:      "app.example.com",
		TargetOwner:   "alice",
		TargetProject: "myproject",
		TargetSandbox: "codex",
		TargetIP:      "10.248.0.20",
		RoutePort:     5173,
	})
	if err != nil {
		t.Fatal(err)
	}
	resource := &fakeRouteResourceServer{profiles: map[string]*api.Profile{
		"sc-route-app-example-com": {Name: "sc-route-app-example-com", ProfilePut: api.ProfilePut{Config: api.ConfigMap(metadata)}},
	}}
	target := &fakeRouteResourceServer{instance: &api.Instance{Name: "sc-codex", InstancePut: api.InstancePut{Devices: api.DevicesMap{
		"eth0":                     {"type": "nic"},
		"sc-route-app-example-com": {"type": "nic", "parent": "sc-private"},
	}}}}
	server := &fakeRouteServer{
		resource:        resource,
		targetResource:  target,
		infrastructure:  "sc-infra",
		projectMetadata: projectMetadataForRouteTest(t, []meta.PublicRoute{{Hostname: "app.example.com", Sandbox: "codex", RoutePort: 5173}}),
	}
	manager := RouteManager{Server: server}
	if err := manager.Remove(context.Background(), route.RemovePlan{Hostname: "app.example.com", InfrastructureProject: "sc-infra", ProjectPrefix: "sc"}); err != nil {
		t.Fatal(err)
	}
	if resource.deletedProfile != "sc-route-app-example-com" {
		t.Fatalf("deleted profile = %q", resource.deletedProfile)
	}
	if _, ok := resource.createdFiles["sc-caddy:/etc/caddy/Caddyfile"]; !ok {
		t.Fatal("expected infrastructure Caddyfile rewrite")
	}
	if target.updated == nil {
		t.Fatal("expected target sandbox ingress device removal")
	}
	if target.updated.Devices["sc-route-app-example-com"] != nil {
		t.Fatalf("devices = %#v", target.updated.Devices)
	}
	if server.updatedProject == nil {
		t.Fatal("expected target project route backlink update")
	}
	projectMetadata, err := meta.ParseProjectConfig(map[string]string(server.updatedProject.Config))
	if err != nil {
		t.Fatal(err)
	}
	if len(projectMetadata.PublicRoutes) != 0 {
		t.Fatalf("public routes = %#v", projectMetadata.PublicRoutes)
	}
}

func TestRouteManagerRemovesRouteProfileWhenTargetSandboxIsGone(t *testing.T) {
	metadata, err := meta.RouteConfig(meta.Route{
		Hostname:      "app.example.com",
		TargetOwner:   "alice",
		TargetProject: "myproject",
		TargetSandbox: "codex",
		TargetIP:      "10.248.0.20",
		RoutePort:     5173,
	})
	if err != nil {
		t.Fatal(err)
	}
	resource := &fakeRouteResourceServer{profiles: map[string]*api.Profile{
		"sc-route-app-example-com": {Name: "sc-route-app-example-com", ProfilePut: api.ProfilePut{Config: api.ConfigMap(metadata)}},
	}}
	target := &fakeRouteResourceServer{}
	manager := RouteManager{Server: &fakeRouteServer{resource: resource, targetResource: target, infrastructure: "sc-infra"}}
	if err := manager.Remove(context.Background(), route.RemovePlan{Hostname: "app.example.com", InfrastructureProject: "sc-infra", ProjectPrefix: "sc"}); err != nil {
		t.Fatal(err)
	}
	if resource.deletedProfile != "sc-route-app-example-com" {
		t.Fatalf("deleted profile = %q", resource.deletedProfile)
	}
	if target.updated != nil {
		t.Fatalf("target sandbox should not be updated when missing: %#v", target.updated)
	}
	if _, ok := resource.createdFiles["sc-caddy:/etc/caddy/Caddyfile"]; !ok {
		t.Fatal("expected infrastructure Caddyfile rewrite")
	}
}

func routePlanForTest(t *testing.T) route.AddPlan {
	t.Helper()
	metadata, err := meta.RouteConfig(meta.Route{
		Hostname:      "app.example.com",
		TargetOwner:   "alice",
		TargetProject: "myproject",
		TargetSandbox: "codex",
		TargetIP:      "10.248.0.20",
		RoutePort:     5173,
	})
	if err != nil {
		t.Fatal(err)
	}
	return route.AddPlan{
		Hostname:              "app.example.com",
		TargetReference:       "alice/myproject/codex",
		Project:               projectSummaryForRouteTest(),
		Sandbox:               meta.Sandbox{Name: "codex"},
		TargetInstanceName:    "sc-codex",
		InfrastructureProject: "sc-infra",
		RoutePort:             5173,
		IngressDevice:         "sc-route-app-example-com",
		IngressNetwork:        "sc-private",
		MetadataConfig:        metadata,
		DNSProof: route.DNSProof{
			Required:       true,
			Hostname:       "app.example.com",
			ExpectedTarget: "203.0.113.10",
		},
	}
}

func projectSummaryForRouteTest() project.Summary {
	return project.Summary{IncusName: "sc-alice-myproject", Owner: "alice", Name: "myproject"}
}

func projectMetadataForRouteTest(t *testing.T, publicRoutes []meta.PublicRoute) *api.Project {
	t.Helper()
	config, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
		PublicRoutes:    publicRoutes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &api.Project{Name: "sc-alice-myproject", ProjectPut: api.ProjectPut{Config: api.ConfigMap(config)}}
}

type fakeRouteDNSResolver struct {
	hosts []string
}

func (r fakeRouteDNSResolver) LookupHost(ctx context.Context, hostname string) ([]string, error) {
	return r.hosts, nil
}
