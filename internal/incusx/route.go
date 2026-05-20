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
	GetProject(name string) (*api.Project, string, error)
	UpdateProject(name string, project api.ProjectPut, etag string) error
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
	Remote                string
	ConfigPath            string
	InfrastructureProject string
	LetsEncryptEmail      string
	Server                RouteServer
	Resolver              route.DNSResolver
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
	existing, _, err := projectServer.GetProfile(name)
	if err == nil {
		if existing.Config[meta.KeyKind] == meta.KindRoute {
			routeMetadata, parseErr := meta.ParseRouteConfig(map[string]string(existing.Config))
			if parseErr != nil {
				return fmt.Errorf("parse existing route metadata for %s: %w", plan.Hostname, parseErr)
			}
			return route.NewConflictError("public route hostname %s is already claimed by %s/%s/%s", plan.Hostname, routeMetadata.TargetOwner, routeMetadata.TargetProject, routeMetadata.TargetSandbox)
		}
		return route.NewConflictError("public route hostname %s conflicts with existing infrastructure profile %s", plan.Hostname, name)
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get route metadata %s: %w", plan.Hostname, err)
	}
	if _, err := route.VerifyDNSProof(ctx, m.Resolver, plan.DNSProof); err != nil {
		return err
	}
	targetProjectServer := server.UseProject(plan.Project.IncusName)
	if err := ensureRouteIngressAttachment(targetProjectServer, plan); err != nil {
		return err
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
	if err := addRouteBacklink(server, plan); err != nil {
		return err
	}
	return refreshInfrastructureCaddy(projectServer, m.LetsEncryptEmail)
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
	if api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil
	}
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
		if err := removeRouteBacklink(server, plan, routeMetadata); err != nil {
			return err
		}
	}
	if err := projectServer.DeleteProfile(route.ProfileName(plan.Hostname)); err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("delete route metadata %s: %w", plan.Hostname, err)
	}
	return refreshInfrastructureCaddy(projectServer, m.LetsEncryptEmail)
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

func (m RouteManager) FindRoute(ctx context.Context, hostname string) (meta.Route, error) {
	server, err := m.server()
	if err != nil {
		return meta.Route{}, err
	}
	infrastructureProject := strings.TrimSpace(m.InfrastructureProject)
	if infrastructureProject == "" {
		return meta.Route{}, fmt.Errorf("infrastructure project is required")
	}
	return routeMetadataByHostname(server.UseProject(infrastructureProject), hostname)
}

func addRouteBacklink(server RouteServer, plan route.AddPlan) error {
	return updateProjectRoutes(server, plan.Project.IncusName, false, func(routes []meta.PublicRoute) []meta.PublicRoute {
		next := make([]meta.PublicRoute, 0, len(routes)+1)
		for _, existing := range routes {
			if existing.Hostname == plan.Hostname {
				continue
			}
			next = append(next, existing)
		}
		next = append(next, meta.PublicRoute{
			Hostname:  plan.Hostname,
			Sandbox:   plan.Sandbox.Name,
			RoutePort: plan.RoutePort,
		})
		sort.Slice(next, func(i, j int) bool {
			return next[i].Hostname < next[j].Hostname
		})
		return next
	})
}

func removeRouteBacklink(server RouteServer, plan route.RemovePlan, routeMetadata meta.Route) error {
	projectRef := naming.ProjectRef{Owner: routeMetadata.TargetOwner, Project: routeMetadata.TargetProject}
	incusProject, err := naming.IncusProjectNameWithPrefix(plan.ProjectPrefix, projectRef)
	if err != nil {
		return err
	}
	return updateProjectRoutes(server, incusProject, true, func(routes []meta.PublicRoute) []meta.PublicRoute {
		next := make([]meta.PublicRoute, 0, len(routes))
		for _, existing := range routes {
			if existing.Hostname == routeMetadata.Hostname {
				continue
			}
			next = append(next, existing)
		}
		return next
	})
}

func updateProjectRoutes(server RouteServer, projectName string, tolerateMissing bool, update func([]meta.PublicRoute) []meta.PublicRoute) error {
	incusProject, etag, err := server.GetProject(projectName)
	if api.StatusErrorCheck(err, http.StatusNotFound) && tolerateMissing {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get project route backlinks for %s: %w", projectName, err)
	}
	managed, err := meta.ParseProjectConfig(map[string]string(incusProject.Config))
	if err != nil {
		return fmt.Errorf("parse project route backlinks for %s: %w", projectName, err)
	}
	managed.PublicRoutes = update(managed.PublicRoutes)
	config, err := meta.ProjectConfig(managed)
	if err != nil {
		return err
	}
	put := incusProject.Writable()
	if put.Config == nil {
		put.Config = api.ConfigMap{}
	}
	for key, value := range config {
		put.Config[key] = value
	}
	if err := server.UpdateProject(projectName, put, etag); err != nil {
		return fmt.Errorf("update project route backlinks for %s: %w", projectName, err)
	}
	return nil
}

func refreshInfrastructureCaddy(server RouteResourceServer, letsEncryptEmail string) error {
	routes, err := listRouteMetadata(server)
	if err != nil {
		return err
	}
	file := caddy.RenderInfrastructureWithOptions(routes, caddy.InfrastructureOptions{LetsEncryptEmail: letsEncryptEmail})
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

func (s sdkRouteServer) GetProject(name string) (*api.Project, string, error) {
	return s.inner.GetProject(name)
}

func (s sdkRouteServer) UpdateProject(name string, project api.ProjectPut, etag string) error {
	return s.inner.UpdateProject(name, project, etag)
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
