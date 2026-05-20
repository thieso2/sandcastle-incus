package route

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
)

type AddRequest struct {
	Hostname        string
	TargetReference string
}

type RemoveRequest struct {
	Hostname string
}

type AddPlan struct {
	Hostname              string            `json:"hostname"`
	TargetReference       string            `json:"targetReference"`
	Project               project.Summary   `json:"project"`
	Sandbox               meta.Sandbox      `json:"sandbox"`
	InfrastructureProject string            `json:"infrastructureProject"`
	RoutePort             int               `json:"routePort"`
	TargetIP              string            `json:"targetIP"`
	MetadataConfig        map[string]string `json:"metadataConfig"`
	RequiresBroker        bool              `json:"requiresBroker"`
	DNSProof              string            `json:"dnsProof"`
}

type RemovePlan struct {
	Hostname              string `json:"hostname"`
	InfrastructureProject string `json:"infrastructureProject"`
	RequiresBroker        bool   `json:"requiresBroker"`
}

type ListPlan struct {
	InfrastructureProject string `json:"infrastructureProject"`
	RequiresBroker        bool   `json:"requiresBroker"`
}

type Route struct {
	Hostname        string `json:"hostname"`
	TargetReference string `json:"targetReference"`
	RoutePort       int    `json:"routePort"`
}

type ListResult struct {
	Routes []Route `json:"routes"`
}

type SandboxStore interface {
	FindSandbox(ctx context.Context, project project.Summary, name string) (meta.Sandbox, error)
}

type Manager interface {
	Add(context.Context, AddPlan) error
	Remove(context.Context, RemovePlan) error
	List(context.Context, ListPlan) (ListResult, error)
}

func PlanAdd(ctx context.Context, admin config.Admin, projectStore project.IncusProjectStore, sandboxStore SandboxStore, request AddRequest) (AddPlan, error) {
	if err := admin.Validate(); err != nil {
		return AddPlan{}, err
	}
	hostname, err := normalizePublicHostname(request.Hostname)
	if err != nil {
		return AddPlan{}, err
	}
	projectRef, sandboxName, err := parseSandboxRef(request.TargetReference)
	if err != nil {
		return AddPlan{}, err
	}
	summary, err := findProject(ctx, projectStore, projectRef)
	if err != nil {
		return AddPlan{}, err
	}
	if sandboxStore == nil {
		return AddPlan{}, fmt.Errorf("sandbox metadata store is required")
	}
	target, err := sandboxStore.FindSandbox(ctx, summary, sandboxName)
	if err != nil {
		return AddPlan{}, err
	}
	if target.PrivateIP == "" {
		return AddPlan{}, fmt.Errorf("sandbox %s has no private IP", request.TargetReference)
	}
	routePort := target.AppPort
	if routePort == 0 {
		routePort = sandbox.DefaultAppPort
	}
	routeMetadata := meta.Route{
		Hostname:      hostname,
		TargetOwner:   summary.Owner,
		TargetProject: summary.Name,
		TargetSandbox: target.Name,
		TargetIP:      target.PrivateIP,
		RoutePort:     routePort,
	}
	metadataConfig, err := meta.RouteConfig(routeMetadata)
	if err != nil {
		return AddPlan{}, err
	}
	return AddPlan{
		Hostname:              hostname,
		TargetReference:       summary.Owner + "/" + summary.Name + "/" + target.Name,
		Project:               summary,
		Sandbox:               target,
		InfrastructureProject: admin.InfrastructureProject,
		RoutePort:             routePort,
		TargetIP:              target.PrivateIP,
		MetadataConfig:        metadataConfig,
		RequiresBroker:        true,
		DNSProof:              "Broker must verify public DNS points at Sandcastle infrastructure before accepting this route.",
	}, nil
}

func PlanRemove(admin config.Admin, request RemoveRequest) (RemovePlan, error) {
	if err := admin.Validate(); err != nil {
		return RemovePlan{}, err
	}
	hostname, err := normalizePublicHostname(request.Hostname)
	if err != nil {
		return RemovePlan{}, err
	}
	return RemovePlan{
		Hostname:              hostname,
		InfrastructureProject: admin.InfrastructureProject,
		RequiresBroker:        true,
	}, nil
}

func PlanList(admin config.Admin) (ListPlan, error) {
	if err := admin.Validate(); err != nil {
		return ListPlan{}, err
	}
	return ListPlan{InfrastructureProject: admin.InfrastructureProject, RequiresBroker: true}, nil
}

func normalizePublicHostname(value string) (string, error) {
	hostname := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if hostname == "" {
		return "", fmt.Errorf("hostname is required")
	}
	if strings.Contains(hostname, "*") {
		return "", fmt.Errorf("wildcard public routes are not supported")
	}
	if strings.Contains(hostname, "/") || net.ParseIP(hostname) != nil {
		return "", fmt.Errorf("route hostname must be an exact DNS hostname")
	}
	labels := strings.Split(hostname, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("route hostname must be fully qualified")
	}
	for _, label := range labels {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("invalid route hostname %q", value)
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return "", fmt.Errorf("invalid route hostname %q", value)
		}
	}
	return hostname, nil
}

func parseSandboxRef(value string) (naming.ProjectRef, string, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 3 {
		return naming.ProjectRef{}, "", fmt.Errorf("route target must be owner/project/name")
	}
	projectRef, err := naming.ParseProjectRef(parts[0] + "/" + parts[1])
	if err != nil {
		return naming.ProjectRef{}, "", err
	}
	if err := (naming.ProjectRef{Owner: parts[2], Project: "placeholder"}).Validate(); err != nil {
		return naming.ProjectRef{}, "", fmt.Errorf("invalid sandbox name %q", parts[2])
	}
	return projectRef, parts[2], nil
}

func findProject(ctx context.Context, store project.IncusProjectStore, ref naming.ProjectRef) (project.Summary, error) {
	projects, err := project.List(ctx, store)
	if err != nil {
		return project.Summary{}, err
	}
	for _, summary := range projects {
		if summary.Owner == ref.Owner && summary.Name == ref.Project {
			return summary, nil
		}
	}
	return project.Summary{}, fmt.Errorf("project %q not found", ref.String())
}
