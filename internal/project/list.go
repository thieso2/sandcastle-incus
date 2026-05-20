package project

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

type IncusProjectStore interface {
	ListProjects(ctx context.Context) ([]IncusProject, error)
}

type Summary struct {
	IncusName       string             `json:"incusName"`
	Owner           string             `json:"owner"`
	Name            string             `json:"name"`
	Domain          string             `json:"domain,omitempty"`
	PrivateCIDR     string             `json:"privateCIDR,omitempty"`
	DNSAddress      string             `json:"dnsAddress,omitempty"`
	DefaultTemplate string             `json:"defaultTemplate,omitempty"`
	SSHPublicKey    string             `json:"sshPublicKey,omitempty"`
	Status          string             `json:"status"`
	Tailscale       meta.Tailscale     `json:"tailscale,omitempty"`
	PublicRoutes    []meta.PublicRoute `json:"publicRoutes,omitempty"`
}

func List(ctx context.Context, store IncusProjectStore) ([]Summary, error) {
	projects, err := store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	summaries := make([]Summary, 0, len(projects))
	for _, incusProject := range projects {
		if !meta.IsManaged(incusProject.Config) {
			continue
		}
		project, err := meta.ParseProjectConfig(incusProject.Config)
		if err != nil {
			return nil, fmt.Errorf("parse project metadata for %s: %w", incusProject.Name, err)
		}
		summaries = append(summaries, Summary{
			IncusName:       incusProject.Name,
			Owner:           project.Owner,
			Name:            project.Project,
			Domain:          project.Domain,
			PrivateCIDR:     project.PrivateCIDR,
			DNSAddress:      dnsAddressFromCIDR(project.PrivateCIDR),
			DefaultTemplate: project.DefaultTemplate,
			SSHPublicKey:    project.SSHPublicKey,
			Status:          "managed",
			Tailscale:       project.Tailscale,
			PublicRoutes:    append([]meta.PublicRoute{}, project.PublicRoutes...),
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Owner == summaries[j].Owner {
			return summaries[i].Name < summaries[j].Name
		}
		return summaries[i].Owner < summaries[j].Owner
	})
	return summaries, nil
}

func OccupiedCIDRs(projects []Summary) []string {
	cidrs := make([]string, 0, len(projects))
	for _, project := range projects {
		if project.PrivateCIDR != "" {
			cidrs = append(cidrs, project.PrivateCIDR)
		}
	}
	return cidrs
}

func dnsAddressFromCIDR(privateCIDR string) string {
	prefix, err := netip.ParsePrefix(privateCIDR)
	if err != nil {
		return ""
	}
	addr, err := cidr.RoleAddress(prefix, 53)
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
