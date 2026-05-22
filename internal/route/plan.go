package route

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

const InfrastructureCaddyName = "sc-caddy"

type CreateRequest struct {
	Hostname        string
	TargetReference string
}

type DeleteRequest struct {
	Hostname string
}

type StatusRequest struct {
	Hostname string
}

type CreatePlan struct {
	Hostname              string            `json:"hostname"`
	TargetReference       string            `json:"targetReference"`
	Tenant                tenant.Summary    `json:"tenant"`
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

type DeletePlan struct {
	Hostname              string `json:"hostname"`
	InfrastructureProject string `json:"infrastructureProject"`
	IncusProjectPrefix    string `json:"incusProjectPrefix"`
	RequiresBroker        bool   `json:"requiresBroker"`
}

type ListPlan struct {
	InfrastructureProject string `json:"infrastructureProject"`
	RequiresBroker        bool   `json:"requiresBroker"`
}

type StatusPlan struct {
	Hostname              string `json:"hostname"`
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
	FindMachine(ctx context.Context, tenant tenant.Summary, projectName string, machineName string) (meta.Machine, error)
}

type Manager interface {
	Create(context.Context, CreatePlan) error
	Delete(context.Context, DeletePlan) error
	List(context.Context, ListPlan) (ListResult, error)
}

func PlanCreate(ctx context.Context, admin config.Admin, tenantStore tenant.IncusTenantStore, machineStore MachineStore, request CreateRequest) (CreatePlan, error) {
	if err := admin.Validate(); err != nil {
		return CreatePlan{}, err
	}
	infrastructureHost := strings.TrimSpace(admin.InfrastructureHost)
	if infrastructureHost == "" {
		return CreatePlan{}, fmt.Errorf("infrastructure host is required for public route DNS proof")
	}
	hostname, err := normalizePublicHostname(request.Hostname)
	if err != nil {
		return CreatePlan{}, err
	}
	authHostname := strings.ToLower(strings.Trim(strings.TrimSpace(admin.AuthHostname), "."))
	if authHostname != "" && hostname == strings.ToLower(authHostname) {
		return CreatePlan{}, fmt.Errorf("auth hostname %s is reserved infrastructure routing and cannot be claimed as a public route", hostname)
	}
	machineRef, err := parseMachineRef(request.TargetReference, admin.Tenant, admin.Project)
	if err != nil {
		return CreatePlan{}, err
	}
	summary, err := findTenant(ctx, tenantStore, machineRef.Tenant)
	if err != nil {
		return CreatePlan{}, err
	}
	if machineStore == nil {
		return CreatePlan{}, fmt.Errorf("machine metadata store is required")
	}
	target, err := machineStore.FindMachine(ctx, summary, machineRef.Project, machineRef.Machine)
	if err != nil {
		return CreatePlan{}, err
	}
	if target.PrivateIP == "" {
		return CreatePlan{}, fmt.Errorf("machine %s has no private IP", request.TargetReference)
	}
	routePort := target.AppPort
	if routePort == 0 {
		template := target.Template
		if template == "" {
			template = summary.DefaultTemplate
		}
		routePort, err = machine.DefaultAppPortForTemplate(template)
		if err != nil {
			return CreatePlan{}, err
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
		return CreatePlan{}, err
	}
	instanceName, err := naming.MachineIncusInstanceName(naming.MachineRef{Tenant: summary.Tenant, Project: target.Project, Machine: target.Name})
	if err != nil {
		return CreatePlan{}, err
	}
	canonicalReference := naming.MachineRef{Tenant: summary.Tenant, Project: target.Project, Machine: target.Name}.String()
	return CreatePlan{
		Hostname:              hostname,
		TargetReference:       canonicalReference,
		Tenant:                summary,
		Machine:               target,
		TargetInstanceName:    instanceName,
		InfrastructureProject: admin.InfrastructureProject,
		RoutePort:             routePort,
		TargetIP:              target.PrivateIP,
		IngressDevice:         ProfileName(hostname),
		IngressNetwork:        tenant.PrivateNetworkName(summary.IncusName),
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

func PlanDelete(admin config.Admin, request DeleteRequest) (DeletePlan, error) {
	if err := admin.Validate(); err != nil {
		return DeletePlan{}, err
	}
	hostname, err := normalizePublicHostname(request.Hostname)
	if err != nil {
		return DeletePlan{}, err
	}
	return DeletePlan{
		Hostname:              hostname,
		InfrastructureProject: admin.InfrastructureProject,
		IncusProjectPrefix:    admin.IncusProjectPrefix,
		RequiresBroker:        true,
	}, nil
}

func PlanList(admin config.Admin) (ListPlan, error) {
	if err := admin.Validate(); err != nil {
		return ListPlan{}, err
	}
	return ListPlan{InfrastructureProject: admin.InfrastructureProject, RequiresBroker: true}, nil
}

func PlanStatus(admin config.Admin, request StatusRequest) (StatusPlan, error) {
	if err := admin.Validate(); err != nil {
		return StatusPlan{}, err
	}
	hostname, err := normalizePublicHostname(request.Hostname)
	if err != nil {
		return StatusPlan{}, err
	}
	return StatusPlan{
		Hostname:              hostname,
		InfrastructureProject: admin.InfrastructureProject,
		RequiresBroker:        true,
	}, nil
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

func findTenant(ctx context.Context, store tenant.IncusTenantStore, tenantName string) (tenant.Summary, error) {
	projects, err := tenant.List(ctx, store)
	if err != nil {
		return tenant.Summary{}, err
	}
	for _, summary := range projects {
		if summary.Tenant == tenantName {
			return summary, nil
		}
	}
	return tenant.Summary{}, fmt.Errorf("tenant %q not found", tenantName)
}
