package route

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

const InfrastructureCaddyName = "sc-caddy"

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
	Tenant                project.Summary   `json:"tenant"`
	Machine               meta.Machine      `json:"machine"`
	TargetInstanceName    string            `json:"targetInstanceName"`
	InfrastructureProject string            `json:"infrastructureProject"`
	RoutePort             int               `json:"routePort"`
	TargetIP              string            `json:"targetIP"`
	IngressDevice         string            `json:"ingressDevice"`
	IngressNetwork        string            `json:"ingressNetwork"`
	MetadataConfig        map[string]string `json:"metadataConfig"`
	RequiresBroker        bool              `json:"requiresBroker"`
	DNSProof              DNSProof          `json:"dnsProof"`
}

type DNSProof struct {
	Required        bool     `json:"required"`
	Hostname        string   `json:"hostname"`
	ExpectedTarget  string   `json:"expectedTarget,omitempty"`
	ResolvedTargets []string `json:"resolvedTargets,omitempty"`
	Message         string   `json:"message"`
}

type RemovePlan struct {
	Hostname              string `json:"hostname"`
	InfrastructureProject string `json:"infrastructureProject"`
	ProjectPrefix         string `json:"projectPrefix"`
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

type MachineStore interface {
	FindMachine(ctx context.Context, tenant project.Summary, projectName string, machineName string) (meta.Machine, error)
}

type Manager interface {
	Add(context.Context, AddPlan) error
	Remove(context.Context, RemovePlan) error
	List(context.Context, ListPlan) (ListResult, error)
}

func PlanAdd(ctx context.Context, admin config.Admin, projectStore project.IncusProjectStore, machineStore MachineStore, request AddRequest) (AddPlan, error) {
	if err := admin.Validate(); err != nil {
		return AddPlan{}, err
	}
	infrastructureHost := strings.TrimSpace(admin.InfrastructureHost)
	if infrastructureHost == "" {
		return AddPlan{}, fmt.Errorf("infrastructure host is required for public route DNS proof")
	}
	hostname, err := normalizePublicHostname(request.Hostname)
	if err != nil {
		return AddPlan{}, err
	}
	machineRef, err := parseMachineRef(request.TargetReference, admin.Tenant, admin.Project)
	if err != nil {
		return AddPlan{}, err
	}
	summary, err := findTenant(ctx, projectStore, machineRef.Tenant)
	if err != nil {
		return AddPlan{}, err
	}
	if machineStore == nil {
		return AddPlan{}, fmt.Errorf("machine metadata store is required")
	}
	target, err := machineStore.FindMachine(ctx, summary, machineRef.Project, machineRef.Machine)
	if err != nil {
		return AddPlan{}, err
	}
	if target.PrivateIP == "" {
		return AddPlan{}, fmt.Errorf("machine %s has no private IP", request.TargetReference)
	}
	routePort := target.AppPort
	if routePort == 0 {
		template := target.Template
		if template == "" {
			template = summary.DefaultTemplate
		}
		routePort, err = sandbox.DefaultAppPortForTemplate(template)
		if err != nil {
			return AddPlan{}, err
		}
	}
	routeMetadata := meta.Route{
		Hostname:      hostname,
		TargetTenant:  summary.Tenant,
		TargetProject: target.Project,
		TargetMachine: target.Name,
		TargetIP:      target.PrivateIP,
		RoutePort:     routePort,
	}
	metadataConfig, err := meta.RouteConfig(routeMetadata)
	if err != nil {
		return AddPlan{}, err
	}
	instanceName, err := naming.MachineIncusInstanceName(naming.MachineRef{Tenant: summary.Tenant, Project: target.Project, Machine: target.Name})
	if err != nil {
		return AddPlan{}, err
	}
	canonicalReference := naming.MachineRef{Tenant: summary.Tenant, Project: target.Project, Machine: target.Name}.String()
	return AddPlan{
		Hostname:              hostname,
		TargetReference:       canonicalReference,
		Tenant:                summary,
		Machine:               target,
		TargetInstanceName:    instanceName,
		InfrastructureProject: admin.InfrastructureProject,
		RoutePort:             routePort,
		TargetIP:              target.PrivateIP,
		IngressDevice:         ProfileName(hostname),
		IngressNetwork:        project.PrivateNetworkName(summary.IncusName),
		MetadataConfig:        metadataConfig,
		RequiresBroker:        true,
		DNSProof: DNSProof{
			Required:       true,
			Hostname:       hostname,
			ExpectedTarget: infrastructureHost,
			Message:        "Broker must verify public DNS points at Sandcastle infrastructure before accepting this route.",
		},
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
		ProjectPrefix:         admin.ProjectPrefix,
		RequiresBroker:        true,
	}, nil
}

func PlanList(admin config.Admin) (ListPlan, error) {
	if err := admin.Validate(); err != nil {
		return ListPlan{}, err
	}
	return ListPlan{InfrastructureProject: admin.InfrastructureProject, RequiresBroker: true}, nil
}

func ProfileName(hostname string) string {
	normalized := strings.NewReplacer(".", "-", "_", "-", ":", "-").Replace(strings.ToLower(strings.TrimSpace(hostname)))
	return "sc-route-" + normalized
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

func parseMachineRef(value string, currentTenant string, currentProject string) (naming.MachineRef, error) {
	if strings.TrimSpace(currentTenant) != "" {
		projectRef, machineName, err := naming.ParseUserMachineRef(value, currentProject)
		if err != nil {
			return naming.MachineRef{}, err
		}
		return naming.MachineRef{Tenant: currentTenant, Project: projectRef.Project, Machine: machineName}, nil
	}
	parts := strings.Split(value, "/")
	if len(parts) == 2 || len(parts) == 3 {
		return naming.ParseAdminMachineRef(value)
	}
	return naming.MachineRef{}, fmt.Errorf("route target must be machine, project/machine, tenant/machine, or tenant/project/machine")
}

func findTenant(ctx context.Context, store project.IncusProjectStore, tenantName string) (project.Summary, error) {
	projects, err := project.List(ctx, store)
	if err != nil {
		return project.Summary{}, err
	}
	for _, summary := range projects {
		if summary.Tenant == tenantName {
			return summary, nil
		}
	}
	return project.Summary{}, fmt.Errorf("tenant %q not found", tenantName)
}
