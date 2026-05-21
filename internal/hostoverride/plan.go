package hostoverride

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type AddRequest struct {
	Reference string
	Hostname  string
}

type DeleteRequest struct {
	Reference string
	Hostname  string
}

type ListRequest struct {
	Reference string
}

type AddPlan struct {
	Reference         string         `json:"reference"`
	Tenant            tenant.Summary `json:"tenant"`
	Machine           meta.Machine   `json:"machine"`
	InstanceName      string         `json:"instanceName"`
	StoragePool       string         `json:"storagePool"`
	CAVolume          string         `json:"caVolume"`
	Hostname          string         `json:"hostname"`
	IPAddress         string         `json:"ipAddress"`
	ExtraSANs         []string       `json:"extraSANs"`
	HostsEntry        HostsEntry     `json:"hostsEntry"`
	TrustWarning      string         `json:"trustWarning"`
	RequiresReissue   bool           `json:"requiresReissue"`
	RequiresHostsEdit bool           `json:"requiresHostsEdit"`
}

type DeletePlan struct {
	Reference         string         `json:"reference"`
	Tenant            tenant.Summary `json:"tenant"`
	Machine           meta.Machine   `json:"machine"`
	InstanceName      string         `json:"instanceName"`
	StoragePool       string         `json:"storagePool"`
	CAVolume          string         `json:"caVolume"`
	Hostname          string         `json:"hostname"`
	HostsEntry        HostsEntry     `json:"hostsEntry"`
	RequiresReissue   bool           `json:"requiresReissue"`
	RequiresHostsEdit bool           `json:"requiresHostsEdit"`
}

type ListResult struct {
	Tenant    tenant.Summary `json:"tenant"`
	Overrides []Override     `json:"overrides"`
}

type Override struct {
	Reference string       `json:"reference"`
	Machine   meta.Machine `json:"machine"`
	Hostname  string       `json:"hostname"`
	IPAddress string       `json:"ipAddress"`
}

type HostsEntry struct {
	BeginLine string `json:"beginLine"`
	Line      string `json:"line"`
	EndLine   string `json:"endLine"`
}

type MachineStore interface {
	FindMachine(ctx context.Context, tenant tenant.Summary, projectName string, machineName string) (meta.Machine, error)
	ListMachines(ctx context.Context, tenant tenant.Summary) ([]meta.Machine, error)
}

type Manager interface {
	Add(context.Context, AddPlan) error
	Delete(context.Context, DeletePlan) error
}

func PlanAdd(ctx context.Context, admin config.Admin, tenantStore tenant.IncusTenantStore, machineStore MachineStore, request AddRequest) (AddPlan, error) {
	if err := admin.Validate(); err != nil {
		return AddPlan{}, err
	}
	machineRef, err := parseMachineRef(request.Reference, admin.Tenant, admin.Project)
	if err != nil {
		return AddPlan{}, err
	}
	hostname, err := normalizeExactHostname(request.Hostname)
	if err != nil {
		return AddPlan{}, err
	}
	summary, err := findTenant(ctx, tenantStore, machineRef.Tenant)
	if err != nil {
		return AddPlan{}, err
	}
	if machineStore == nil {
		return AddPlan{}, fmt.Errorf("machine metadata store is required")
	}
	machine, err := machineStore.FindMachine(ctx, summary, machineRef.Project, machineRef.Machine)
	if err != nil {
		return AddPlan{}, err
	}
	if machine.PrivateIP == "" {
		return AddPlan{}, fmt.Errorf("machine %s has no private IP", request.Reference)
	}
	if err := validateHostnameAvailable(ctx, machineStore, summary, machineRef.Project, machineRef.Machine, hostname); err != nil {
		return AddPlan{}, err
	}
	canonicalReference := naming.MachineRef{Tenant: summary.Tenant, Project: machine.Project, Machine: machine.Name}.String()
	instanceName, err := naming.MachineIncusInstanceName(naming.MachineRef{Tenant: summary.Tenant, Project: machine.Project, Machine: machine.Name})
	if err != nil {
		return AddPlan{}, err
	}
	return AddPlan{
		Reference:         canonicalReference,
		Tenant:            summary,
		Machine:           machine,
		InstanceName:      instanceName,
		StoragePool:       summary.IncusName,
		CAVolume:          tenant.CAVolumeName,
		Hostname:          hostname,
		IPAddress:         machine.PrivateIP,
		ExtraSANs:         []string{hostname},
		HostsEntry:        RenderHostsEntry(canonicalReference, hostname, machine.PrivateIP),
		TrustWarning:      "Trust the tenant CA before relying on HTTPS for this host override.",
		RequiresReissue:   true,
		RequiresHostsEdit: true,
	}, nil
}

func PlanDelete(ctx context.Context, admin config.Admin, tenantStore tenant.IncusTenantStore, machineStore MachineStore, request DeleteRequest) (DeletePlan, error) {
	if err := admin.Validate(); err != nil {
		return DeletePlan{}, err
	}
	machineRef, err := parseMachineRef(request.Reference, admin.Tenant, admin.Project)
	if err != nil {
		return DeletePlan{}, err
	}
	hostname, err := normalizeExactHostname(request.Hostname)
	if err != nil {
		return DeletePlan{}, err
	}
	summary, err := findTenant(ctx, tenantStore, machineRef.Tenant)
	if err != nil {
		return DeletePlan{}, err
	}
	if machineStore == nil {
		return DeletePlan{}, fmt.Errorf("machine metadata store is required")
	}
	machine, err := machineStore.FindMachine(ctx, summary, machineRef.Project, machineRef.Machine)
	if err != nil {
		return DeletePlan{}, err
	}
	canonicalReference := naming.MachineRef{Tenant: summary.Tenant, Project: machine.Project, Machine: machine.Name}.String()
	instanceName, err := naming.MachineIncusInstanceName(naming.MachineRef{Tenant: summary.Tenant, Project: machine.Project, Machine: machine.Name})
	if err != nil {
		return DeletePlan{}, err
	}
	return DeletePlan{
		Reference:         canonicalReference,
		Tenant:            summary,
		Machine:           machine,
		InstanceName:      instanceName,
		StoragePool:       summary.IncusName,
		CAVolume:          tenant.CAVolumeName,
		Hostname:          hostname,
		HostsEntry:        RenderHostsEntry(canonicalReference, hostname, machine.PrivateIP),
		RequiresReissue:   true,
		RequiresHostsEdit: true,
	}, nil
}

func PlanList(ctx context.Context, admin config.Admin, tenantStore tenant.IncusTenantStore, machineStore MachineStore, request ListRequest) (ListResult, error) {
	if err := admin.Validate(); err != nil {
		return ListResult{}, err
	}
	tenantRef, err := tenantRef(request.Reference, admin.Tenant)
	if err != nil {
		return ListResult{}, err
	}
	summary, err := findTenant(ctx, tenantStore, tenantRef.Tenant)
	if err != nil {
		return ListResult{}, err
	}
	if machineStore == nil {
		return ListResult{}, fmt.Errorf("machine metadata store is required")
	}
	machines, err := machineStore.ListMachines(ctx, summary)
	if err != nil {
		return ListResult{}, err
	}
	result := ListResult{Tenant: summary}
	for _, machine := range machines {
		for _, hostname := range machine.ExtraSANs {
			result.Overrides = append(result.Overrides, Override{
				Reference: naming.MachineRef{Tenant: summary.Tenant, Project: machine.Project, Machine: machine.Name}.String(),
				Machine:   machine,
				Hostname:  hostname,
				IPAddress: machine.PrivateIP,
			})
		}
	}
	return result, nil
}

func RenderHostsEntry(reference string, hostname string, ipAddress string) HostsEntry {
	id := strings.ToLower(strings.TrimSpace(reference)) + " " + strings.ToLower(strings.TrimSpace(hostname))
	return HostsEntry{
		BeginLine: "# sandcastle host-override begin " + id,
		Line:      strings.TrimSpace(ipAddress) + " " + strings.ToLower(strings.TrimSpace(hostname)),
		EndLine:   "# sandcastle host-override end " + id,
	}
}

func validateHostnameAvailable(ctx context.Context, machineStore MachineStore, summary tenant.Summary, projectName string, machineName string, hostname string) error {
	machines, err := machineStore.ListMachines(ctx, summary)
	if err != nil {
		return err
	}
	for _, machine := range machines {
		if machine.Project == projectName && machine.Name == machineName {
			continue
		}
		for _, existing := range machine.ExtraSANs {
			if strings.EqualFold(existing, hostname) {
				return fmt.Errorf("host override %s is already assigned to %s/%s/%s", hostname, summary.Tenant, machine.Project, machine.Name)
			}
		}
	}
	return nil
}

func normalizeExactHostname(value string) (string, error) {
	hostname := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if hostname == "" {
		return "", fmt.Errorf("hostname is required")
	}
	if strings.Contains(hostname, "*") {
		return "", fmt.Errorf("wildcard host overrides are not supported")
	}
	if strings.Contains(hostname, "/") || net.ParseIP(hostname) != nil {
		return "", fmt.Errorf("host override must be an exact DNS hostname")
	}
	labels := strings.Split(hostname, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("host override must be a fully qualified hostname")
	}
	for _, label := range labels {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("invalid hostname %q", value)
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return "", fmt.Errorf("invalid hostname %q", value)
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
	return naming.MachineRef{}, fmt.Errorf("machine reference must be machine, project/machine, tenant/machine, or tenant/project/machine")
}

func tenantRef(reference string, currentTenant string) (naming.TenantRef, error) {
	value := strings.TrimSpace(reference)
	if value == "" {
		value = strings.TrimSpace(currentTenant)
	}
	if value == "" {
		return naming.TenantRef{}, fmt.Errorf("tenant reference is required")
	}
	return naming.ParseTenantRef(value)
}

func findTenant(ctx context.Context, store tenant.IncusTenantStore, tenantName string) (tenant.Summary, error) {
	tenants, err := tenant.List(ctx, store)
	if err != nil {
		return tenant.Summary{}, err
	}
	for _, summary := range tenants {
		if summary.Tenant == tenantName {
			return summary, nil
		}
	}
	return tenant.Summary{}, fmt.Errorf("Sandcastle tenant %s not found", tenantName)
}
