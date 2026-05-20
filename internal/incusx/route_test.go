package incusx

import (
	"context"
	"net/http"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

type fakeRouteServer struct {
	resource *fakeRouteResourceServer
	project  string
}

func (s *fakeRouteServer) UseProject(name string) RouteResourceServer {
	s.project = name
	return s.resource
}

type fakeRouteResourceServer struct {
	profiles       map[string]*api.Profile
	createdProfile *api.ProfilesPost
	updatedProfile *api.ProfilePut
	deletedProfile string
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

func TestRouteManagerCreatesRouteProfile(t *testing.T) {
	resource := &fakeRouteResourceServer{profiles: map[string]*api.Profile{}}
	manager := RouteManager{Server: &fakeRouteServer{resource: resource}}
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

func TestRouteManagerRemovesRouteProfile(t *testing.T) {
	resource := &fakeRouteResourceServer{profiles: map[string]*api.Profile{
		"sc-route-app-example-com": {Name: "sc-route-app-example-com"},
	}}
	manager := RouteManager{Server: &fakeRouteServer{resource: resource}}
	if err := manager.Remove(context.Background(), route.RemovePlan{Hostname: "app.example.com", InfrastructureProject: "sc-infra"}); err != nil {
		t.Fatal(err)
	}
	if resource.deletedProfile != "sc-route-app-example-com" {
		t.Fatalf("deleted profile = %q", resource.deletedProfile)
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
		InfrastructureProject: "sc-infra",
		MetadataConfig:        metadata,
	}
}
