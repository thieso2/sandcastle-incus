package incusx

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/caddy"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

type RouteServer interface {
	UseProject(name string) RouteResourceServer
}

type RouteResourceServer interface {
	GetInstance(name string) (*api.Instance, string, error)
	UpdateInstance(name string, instance api.InstancePut, ETag string) (incus.Operation, error)
	GetProfile(name string) (*api.Profile, string, error)
	GetProfiles() ([]api.Profile, error)
	CreateProfile(profile api.ProfilesPost) error
	UpdateProfile(name string, profile api.ProfilePut, ETag string) error
	DeleteProfile(name string) error
	CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
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
	targetProjectServer := server.UseProject(plan.Project.IncusName)
	if err := ensureRouteIngressAttachment(targetProjectServer, plan); err != nil {
		return err
	}
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
		return refreshInfrastructureCaddy(projectServer)
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get route metadata %s: %w", plan.Hostname, err)
	}
	if err := projectServer.CreateProfile(api.ProfilesPost{
		Name: name,
		ProfilePut: api.ProfilePut{
			Description: "Sandcastle public route " + plan.Hostname,
			Config:      api.ConfigMap(plan.MetadataConfig),
		},
	}); err != nil {
		return err
	}
	return refreshInfrastructureCaddy(projectServer)
}

func ensureRouteIngressAttachment(server RouteResourceServer, plan route.AddPlan) error {
	instance, etag, err := server.GetInstance(plan.TargetInstanceName)
	if err != nil {
		return fmt.Errorf("get route target sandbox %s: %w", plan.TargetReference, err)
	}
	put := instance.Writable()
	devices := api.DevicesMap{}
	for name, device := range put.Devices {
		copied := map[string]string{}
		for key, value := range device {
			copied[key] = value
		}
		devices[name] = copied
	}
	if devices[plan.IngressDevice] == nil {
		devices[plan.IngressDevice] = map[string]string{
			"type":           "nic",
			"nictype":        "bridged",
			"parent":         plan.IngressNetwork,
			meta.KeyKind:     "route-ingress",
			meta.KeyHostname: plan.Hostname,
			meta.KeyVersion:  "1",
			meta.KeyOwner:    plan.Project.Owner,
			meta.KeyProject:  plan.Project.Name,
			meta.KeyName:     plan.Sandbox.Name,
		}
		put.Devices = devices
		op, err := server.UpdateInstance(plan.TargetInstanceName, put, etag)
		if err != nil {
			return fmt.Errorf("attach route ingress for %s: %w", plan.TargetReference, err)
		}
		if err := op.Wait(); err != nil {
			return fmt.Errorf("wait for route ingress attach for %s: %w", plan.TargetReference, err)
		}
	}
	return nil
}

func removeRouteIngressAttachment(server RouteServer, plan route.RemovePlan, routeMetadata meta.Route) error {
	projectRef := naming.ProjectRef{Owner: routeMetadata.TargetOwner, Project: routeMetadata.TargetProject}
	incusProject, err := naming.IncusProjectNameWithPrefix(plan.ProjectPrefix, projectRef)
	if err != nil {
		return err
	}
	projectServer := server.UseProject(incusProject)
	instanceName := "sc-" + routeMetadata.TargetSandbox
	instance, etag, err := projectServer.GetInstance(instanceName)
	if err != nil {
		return fmt.Errorf("get route target sandbox %s/%s/%s: %w", routeMetadata.TargetOwner, routeMetadata.TargetProject, routeMetadata.TargetSandbox, err)
	}
	put := instance.Writable()
	deviceName := route.ProfileName(plan.Hostname)
	if put.Devices[deviceName] == nil {
		return nil
	}
	devices := api.DevicesMap{}
	for name, device := range put.Devices {
		if name == deviceName {
			continue
		}
		copied := map[string]string{}
		for key, value := range device {
			copied[key] = value
		}
		devices[name] = copied
	}
	put.Devices = devices
	op, err := projectServer.UpdateInstance(instanceName, put, etag)
	if err != nil {
		return fmt.Errorf("remove route ingress for %s: %w", plan.Hostname, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for route ingress removal for %s: %w", plan.Hostname, err)
	}
	return nil
}

func routeMetadataByHostname(server RouteResourceServer, hostname string) (meta.Route, error) {
	profile, _, err := server.GetProfile(route.ProfileName(hostname))
	if err != nil {
		return meta.Route{}, err
	}
	routeMetadata, err := meta.ParseRouteConfig(map[string]string(profile.Config))
	if err != nil {
		return meta.Route{}, fmt.Errorf("parse route metadata for %s: %w", hostname, err)
	}
	return routeMetadata, nil
}

func (m RouteManager) Remove(ctx context.Context, plan route.RemovePlan) error {
	server, err := m.server()
	if err != nil {
		return err
	}
	projectServer := server.UseProject(plan.InfrastructureProject)
	routeMetadata, err := routeMetadataByHostname(projectServer, plan.Hostname)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return err
	}
	if err == nil {
		if err := removeRouteIngressAttachment(server, plan, routeMetadata); err != nil {
			return err
		}
	}
	if err := projectServer.DeleteProfile(route.ProfileName(plan.Hostname)); err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("delete route metadata %s: %w", plan.Hostname, err)
	}
	return refreshInfrastructureCaddy(projectServer)
}

func (m RouteManager) List(ctx context.Context, plan route.ListPlan) (route.ListResult, error) {
	server, err := m.server()
	if err != nil {
		return route.ListResult{}, err
	}
	projectServer := server.UseProject(plan.InfrastructureProject)
	metadataRoutes, err := listRouteMetadata(projectServer)
	if err != nil {
		return route.ListResult{}, err
	}
	routes := []route.Route{}
	for _, routeMetadata := range metadataRoutes {
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

func refreshInfrastructureCaddy(server RouteResourceServer) error {
	routes, err := listRouteMetadata(server)
	if err != nil {
		return err
	}
	file := caddy.RenderInfrastructure(routes)
	if err := server.CreateInstanceFile(route.InfrastructureCaddyName, "/etc/caddy", incus.InstanceFileArgs{Type: "directory", Mode: 0o755}); err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
		return fmt.Errorf("create infrastructure Caddy config directory: %w", err)
	}
	if err := server.CreateInstanceFile(route.InfrastructureCaddyName, file.Path, incus.InstanceFileArgs{
		Content:   strings.NewReader(file.Content),
		Type:      "file",
		Mode:      file.Mode,
		WriteMode: "overwrite",
	}); err != nil {
		return fmt.Errorf("write infrastructure Caddyfile: %w", err)
	}
	if err := reloadInfrastructureCaddy(server); err != nil {
		return err
	}
	return nil
}

func reloadInfrastructureCaddy(server RouteResourceServer) error {
	dataDone := make(chan bool)
	op, err := server.ExecInstance(route.InfrastructureCaddyName, api.InstanceExecPost{
		Command:   []string{"caddy", "reload", "--config", "/etc/caddy/Caddyfile"},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("reload infrastructure Caddy: %w", err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for infrastructure Caddy reload: %w", err)
	}
	<-dataDone
	return nil
}

func listRouteMetadata(server RouteResourceServer) ([]meta.Route, error) {
	profiles, err := server.GetProfiles()
	if err != nil {
		return nil, fmt.Errorf("list route metadata: %w", err)
	}
	routes := []meta.Route{}
	for _, profile := range profiles {
		if profile.Config[meta.KeyKind] != meta.KindRoute {
			continue
		}
		routeMetadata, err := meta.ParseRouteConfig(map[string]string(profile.Config))
		if err != nil {
			return nil, fmt.Errorf("parse route metadata for %s: %w", profile.Name, err)
		}
		routes = append(routes, routeMetadata)
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Hostname < routes[j].Hostname
	})
	return routes, nil
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

func (s sdkRouteResourceServer) GetInstance(name string) (*api.Instance, string, error) {
	return s.inner.GetInstance(name)
}

func (s sdkRouteResourceServer) UpdateInstance(name string, instance api.InstancePut, etag string) (incus.Operation, error) {
	return s.inner.UpdateInstance(name, instance, etag)
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

func (s sdkRouteResourceServer) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
	return s.inner.CreateInstanceFile(instanceName, path, args)
}

func (s sdkRouteResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	return s.inner.ExecInstance(instanceName, exec, args)
}
