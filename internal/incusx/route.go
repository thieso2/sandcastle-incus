package incusx

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

type RouteServer interface {
	UseProject(name string) RouteResourceServer
}

type RouteResourceServer interface {
	GetProfile(name string) (*api.Profile, string, error)
	GetProfiles() ([]api.Profile, error)
	CreateProfile(profile api.ProfilesPost) error
	UpdateProfile(name string, profile api.ProfilePut, ETag string) error
	DeleteProfile(name string) error
}

type RouteManager struct {
	Remote     string
	ConfigPath string
	Server     RouteServer
}

func NewRouteManager(remote string) RouteManager {
	return RouteManager{Remote: remote}
}

func (m RouteManager) Add(ctx context.Context, plan route.AddPlan) error {
	server, err := m.server()
	if err != nil {
		return err
	}
	projectServer := server.UseProject(plan.InfrastructureProject)
	name := route.ProfileName(plan.Hostname)
	existing, etag, err := projectServer.GetProfile(name)
	if err == nil {
		config := mergeConfig(map[string]string(existing.Config), plan.MetadataConfig)
		if err := projectServer.UpdateProfile(name, api.ProfilePut{
			Description: "Sandcastle public route " + plan.Hostname,
			Config:      api.ConfigMap(config),
			Devices:     existing.Devices,
		}, etag); err != nil {
			return fmt.Errorf("update route metadata %s: %w", plan.Hostname, err)
		}
		return nil
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get route metadata %s: %w", plan.Hostname, err)
	}
	return projectServer.CreateProfile(api.ProfilesPost{
		Name: name,
		ProfilePut: api.ProfilePut{
			Description: "Sandcastle public route " + plan.Hostname,
			Config:      api.ConfigMap(plan.MetadataConfig),
		},
	})
}

func (m RouteManager) Remove(ctx context.Context, plan route.RemovePlan) error {
	server, err := m.server()
	if err != nil {
		return err
	}
	projectServer := server.UseProject(plan.InfrastructureProject)
	if err := projectServer.DeleteProfile(route.ProfileName(plan.Hostname)); err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("delete route metadata %s: %w", plan.Hostname, err)
	}
	return nil
}

func (m RouteManager) List(ctx context.Context, plan route.ListPlan) (route.ListResult, error) {
	server, err := m.server()
	if err != nil {
		return route.ListResult{}, err
	}
	projectServer := server.UseProject(plan.InfrastructureProject)
	profiles, err := projectServer.GetProfiles()
	if err != nil {
		return route.ListResult{}, fmt.Errorf("list route metadata: %w", err)
	}
	routes := []route.Route{}
	for _, profile := range profiles {
		if profile.Config[meta.KeyKind] != meta.KindRoute {
			continue
		}
		routeMetadata, err := meta.ParseRouteConfig(map[string]string(profile.Config))
		if err != nil {
			return route.ListResult{}, fmt.Errorf("parse route metadata for %s: %w", profile.Name, err)
		}
		routes = append(routes, route.Route{
			Hostname:        routeMetadata.Hostname,
			TargetReference: routeMetadata.TargetOwner + "/" + routeMetadata.TargetProject + "/" + routeMetadata.TargetSandbox,
			RoutePort:       routeMetadata.RoutePort,
		})
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Hostname < routes[j].Hostname
	})
	return route.ListResult{Routes: routes}, nil
}

func (m RouteManager) server() (RouteServer, error) {
	if m.Server != nil {
		return m.Server, nil
	}
	loaded, err := cliconfig.LoadConfig(m.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load Incus config: %w", err)
	}
	remote := m.Remote
	if remote == "" {
		remote = loaded.DefaultRemote
	}
	instanceServer, err := loaded.GetInstanceServer(remote)
	if err != nil {
		return nil, fmt.Errorf("connect to Incus remote %q: %w", remote, err)
	}
	return sdkRouteServer{inner: instanceServer}, nil
}

type sdkRouteServer struct {
	inner incus.InstanceServer
}

func (s sdkRouteServer) UseProject(name string) RouteResourceServer {
	return sdkRouteResourceServer{inner: s.inner.UseProject(name)}
}

type sdkRouteResourceServer struct {
	inner incus.InstanceServer
}

func (s sdkRouteResourceServer) GetProfile(name string) (*api.Profile, string, error) {
	return s.inner.GetProfile(name)
}

func (s sdkRouteResourceServer) GetProfiles() ([]api.Profile, error) {
	return s.inner.GetProfiles()
}

func (s sdkRouteResourceServer) CreateProfile(profile api.ProfilesPost) error {
	return s.inner.CreateProfile(profile)
}

func (s sdkRouteResourceServer) UpdateProfile(name string, profile api.ProfilePut, etag string) error {
	return s.inner.UpdateProfile(name, profile, etag)
}

func (s sdkRouteResourceServer) DeleteProfile(name string) error {
	return s.inner.DeleteProfile(name)
}
