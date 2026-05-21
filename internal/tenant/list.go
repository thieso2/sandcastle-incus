package tenant

import (
	"context"
	"fmt"
	"net/netip"
	"sort"

	"github.com/thieso2/sandcastle-incus/internal/cidr"
	"github.com/thieso2/sandcastle-incus/internal/meta"
)

type IncusProject struct {
	Name   string
	Config map[string]string
}

type IncusTenantStore interface {
	ListProjects(ctx context.Context) ([]IncusProject, error)
}

type Summary struct {
	IncusName       string             `json:"incusName"`
	Tenant          string             `json:"tenant"`
	DNSSuffix       string             `json:"dnsSuffix,omitempty"`
	PrivateCIDR     string             `json:"privateCIDR,omitempty"`
	DNSAddress      string             `json:"dnsAddress,omitempty"`
	DefaultTemplate string             `json:"defaultTemplate,omitempty"`
	SSHPublicKey    string             `json:"sshPublicKey,omitempty"`
	Projects        []meta.Project     `json:"projects,omitempty"`
	Status          string             `json:"status"`
	Tailscale       meta.Tailscale     `json:"tailscale,omitempty"`
	PublicRoutes    []meta.PublicRoute `json:"publicRoutes,omitempty"`
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
			Tenant:          tenant.Tenant,
			DNSSuffix:       tenant.Tenant,
			PrivateCIDR:     tenant.PrivateCIDR,
			DNSAddress:      dnsAddressFromCIDR(tenant.PrivateCIDR),
			DefaultTemplate: "ai",
			SSHPublicKey:    tenant.SSHPublicKey,
			Projects:        append([]meta.Project{}, tenant.Projects...),
			Status:          "managed",
			Tailscale:       tenant.Tailscale,
			PublicRoutes:    append([]meta.PublicRoute{}, tenant.PublicRoutes...),
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Tenant < summaries[j].Tenant
	})
	return summaries, nil
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
