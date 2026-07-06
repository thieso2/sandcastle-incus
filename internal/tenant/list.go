package tenant

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/cidr"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
)

type IncusProject struct {
	Name   string
	Config map[string]string
}

type IncusTenantStore interface {
	ListProjects(ctx context.Context) ([]IncusProject, error)
}

type Summary struct {
	IncusName       string                    `json:"incusName"`
	InfraProject    string                    `json:"infraProject"`
	Tenant          string                    `json:"tenant"`
	Version         int                       `json:"version,omitempty"`
	Personal        bool                      `json:"personal,omitempty"`
	UnixUser        string                    `json:"unixUser,omitempty"`
	DNSSuffix       string                    `json:"dnsSuffix,omitempty"`
	PrivateCIDR     string                    `json:"privateCIDR,omitempty"`
	DNSAddress      string                    `json:"dnsAddress,omitempty"`
	DefaultTemplate string                    `json:"defaultTemplate,omitempty"`
	SSHPublicKey    string                    `json:"sshPublicKey,omitempty"`
	Projects        []meta.Project            `json:"projects,omitempty"`
	Status          string                    `json:"status"`
	Tailscale       meta.Tailscale            `json:"tailscale,omitempty"`
	PublicRoutes    []meta.PublicRoute        `json:"publicRoutes,omitempty"`
	StorageShares   []meta.TenantStorageShare `json:"storageShares,omitempty"`
}

func List(ctx context.Context, store IncusTenantStore) ([]Summary, error) {
	projects, err := store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	summaries := make([]Summary, 0, len(projects))
	for _, incusProject := range projects {
		if !meta.IsManaged(incusProject.Config) {
			continue
		}
		if incusProject.Config[meta.KeyKind] != meta.KindTenant {
			continue
		}
		tenant, err := meta.ParseTenantConfig(incusProject.Config)
		if err != nil {
			return nil, fmt.Errorf("parse tenant metadata for %s: %w", incusProject.Name, err)
		}
		summaries = append(summaries, Summary{
			IncusName:       incusProject.Name,
			InfraProject:    naming.TenantInfraIncusProjectName(incusProject.Name),
			Tenant:          tenant.Tenant,
			Version:         1,
			Personal:        tenant.Personal,
			UnixUser:        tenant.UnixUser,
			DNSSuffix:       tenant.Tenant,
			PrivateCIDR:     tenant.PrivateCIDR,
			DNSAddress:      dnsAddressFromCIDR(tenant.PrivateCIDR),
			DefaultTemplate: "ai",
			SSHPublicKey:    tenant.SSHPublicKey,
			Projects:        append([]meta.Project{}, tenant.Projects...),
			Status:          "managed",
			Tailscale:       tenant.Tailscale,
			PublicRoutes:    append([]meta.PublicRoute{}, tenant.PublicRoutes...),
			StorageShares:   append([]meta.TenantStorageShare{}, tenant.StorageShares...),
		})
	}
	summaries = append(summaries, v2Summaries(projects)...)
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Tenant < summaries[j].Tenant
	})
	return summaries, nil
}

// v2Summaries builds tenant summaries from v2 per-project Incus projects
// (<prefix>-<tenant>-<project>, kind=project version=2). This is the view a
// tenant's restricted certificate has of its own namespace — the kind=infra
// project (sidecar, CIDR) is usually not visible to it, so the summary is
// assembled from the app projects alone: DNS suffix defaults to the tenant
// name (the v2 default) and the CIDR stays empty.
func v2Summaries(projects []IncusProject) []Summary {
	// The kind=infra projects (visible to admin-socket callers like the
	// auth-app, usually not to restricted tenant certs) carry the tenant's
	// /24 — collect them so the summaries can report it.
	cidrByTenant := map[string]string{}
	suffixByTenant := map[string]string{}
	for _, incusProject := range projects {
		if meta.IsManaged(incusProject.Config) && incusProject.Config[meta.KeyKind] == meta.KindInfra {
			if owner := strings.TrimSpace(incusProject.Config[meta.KeyTenant]); owner != "" {
				cidrByTenant[owner] = strings.TrimSpace(incusProject.Config[meta.KeyV2CIDR])
				suffixByTenant[owner] = strings.TrimSpace(incusProject.Config[meta.KeyV2Suffix])
			}
		}
	}
	byTenant := map[string]*Summary{}
	order := []string{}
	for _, incusProject := range projects {
		if !meta.IsManaged(incusProject.Config) {
			continue
		}
		if incusProject.Config[meta.KeyKind] != meta.KindV2Project || incusProject.Config[meta.KeyVersion] != "2" {
			continue
		}
		tenantName := strings.TrimSpace(incusProject.Config[meta.KeyTenant])
		if tenantName == "" {
			continue
		}
		marker := "-" + tenantName + "-"
		idx := strings.Index(incusProject.Name, marker)
		if idx <= 0 {
			continue
		}
		shortName := incusProject.Name[idx+len(marker):]
		if shortName == "" {
			continue
		}
		summary, seen := byTenant[tenantName]
		if !seen {
			summary = &Summary{
				Tenant:       tenantName,
				Version:      2,
				InfraProject: incusProject.Name[:idx+len("-"+tenantName)],
				DNSSuffix:    firstNonEmptyString(suffixByTenant[tenantName], tenantName),
				PrivateCIDR:  cidrByTenant[tenantName],
				DNSAddress:   dnsAddressFromCIDR(cidrByTenant[tenantName]),
				Status:       "managed",
			}
			byTenant[tenantName] = summary
			order = append(order, tenantName)
		}
		if shortName == naming.DefaultProjectName || summary.IncusName == "" {
			summary.IncusName = incusProject.Name
		}
		summary.Projects = append(summary.Projects, meta.Project{Name: shortName})
	}
	summaries := make([]Summary, 0, len(order))
	for _, tenantName := range order {
		summary := byTenant[tenantName]
		sort.Slice(summary.Projects, func(i, j int) bool { return summary.Projects[i].Name < summary.Projects[j].Name })
		summaries = append(summaries, *summary)
	}
	return summaries
}

// V2IncusProjectName maps a v2 tenant summary and a short project name to the
// full Incus project name, reusing the prefix baked into the summary's
// InfraProject (<prefix>-<tenant>).
func (s Summary) V2IncusProjectName(project string) string {
	project = strings.TrimSpace(project)
	if project == "" {
		project = naming.DefaultProjectName
	}
	return s.InfraProject + "-" + project
}

// AllocatedCIDRs returns every tenant private CIDR currently allocated on the
// host, spanning BOTH v1 tenants (kind=tenant, CIDR in tenant metadata) and v2
// tenants (kind=infra, CIDR in the v2 metadata key). The CIDR allocator feeds
// this in as OccupiedCIDRs so a new tenant never reuses a /24 whose bridge
// already exists — OccupiedCIDRs(List(...)) alone misses v2 tenants, since List
// only surfaces kind=tenant projects.
func AllocatedCIDRs(ctx context.Context, store IncusTenantStore) ([]string, error) {
	_, all, err := CIDRAllocationInputs(ctx, store, "")
	return all, err
}

// CIDRAllocationInputs scans all managed projects and splits allocated CIDRs
// into the target tenant's OWN /24 (empty if it doesn't exist yet) and every
// OTHER tenant's /24. Create uses `own` as PreferredCIDR (so re-provisioning is
// idempotent) and `others` as OccupiedCIDRs (so a fresh tenant avoids
// collisions). Covers both v1 (kind=tenant) and v2 (kind=infra) tenants.
// ProvisionReuseInputs gathers what an idempotent re-provision must reuse from
// live state: the tenant's own /24 and Tenant DNS Suffix (immutable, ADR-0018),
// plus the other tenants' CIDRs the allocator must avoid.
func ProvisionReuseInputs(ctx context.Context, store IncusTenantStore, tenantName string) (ownCIDR string, ownSuffix string, occupied []string, err error) {
	projects, err := store.ListProjects(ctx)
	if err != nil {
		return "", "", nil, err
	}
	tenantName = strings.TrimSpace(tenantName)
	for _, incusProject := range projects {
		if !meta.IsManaged(incusProject.Config) {
			continue
		}
		var cidr, suffix, owner string
		switch incusProject.Config[meta.KeyKind] {
		case meta.KindTenant:
			if t, e := meta.ParseTenantConfig(incusProject.Config); e == nil {
				cidr, owner = strings.TrimSpace(t.PrivateCIDR), strings.TrimSpace(t.Tenant)
			}
		case meta.KindInfra:
			cidr = strings.TrimSpace(incusProject.Config[meta.KeyV2CIDR])
			suffix = strings.TrimSpace(incusProject.Config[meta.KeyV2Suffix])
			owner = strings.TrimSpace(incusProject.Config[meta.KeyTenant])
		default:
			continue
		}
		if cidr == "" {
			continue
		}
		if tenantName != "" && owner == tenantName {
			ownCIDR = cidr
			ownSuffix = suffix
		} else {
			occupied = append(occupied, cidr)
		}
	}
	return ownCIDR, ownSuffix, occupied, nil
}

func CIDRAllocationInputs(ctx context.Context, store IncusTenantStore, tenantName string) (own string, others []string, err error) {
	projects, err := store.ListProjects(ctx)
	if err != nil {
		return "", nil, err
	}
	tenantName = strings.TrimSpace(tenantName)
	for _, incusProject := range projects {
		if !meta.IsManaged(incusProject.Config) {
			continue
		}
		var cidr, owner string
		switch incusProject.Config[meta.KeyKind] {
		case meta.KindTenant:
			if t, e := meta.ParseTenantConfig(incusProject.Config); e == nil {
				cidr, owner = strings.TrimSpace(t.PrivateCIDR), strings.TrimSpace(t.Tenant)
			}
		case meta.KindInfra:
			cidr = strings.TrimSpace(incusProject.Config[meta.KeyV2CIDR])
			owner = strings.TrimSpace(incusProject.Config[meta.KeyTenant])
		default:
			continue
		}
		if cidr == "" {
			continue
		}
		if tenantName != "" && owner == tenantName {
			own = cidr
		} else {
			others = append(others, cidr)
		}
	}
	return own, others, nil
}

func OccupiedCIDRs(tenants []Summary) []string {
	cidrs := make([]string, 0, len(tenants))
	for _, summary := range tenants {
		if summary.PrivateCIDR != "" {
			cidrs = append(cidrs, summary.PrivateCIDR)
		}
	}
	return cidrs
}

func dnsAddressFromCIDR(privateCIDR string) string {
	prefix, err := netip.ParsePrefix(privateCIDR)
	if err != nil {
		return ""
	}
	addr, err := cidr.RoleAddress(prefix, cidr.DNSHostOctet)
	if err != nil {
		return ""
	}
	return addr.String()
}

type MemoryStore struct {
	Projects []IncusProject
}

func (s MemoryStore) ListProjects(ctx context.Context) ([]IncusProject, error) {
	return s.Projects, nil
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
