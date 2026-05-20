package project

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/cidr"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
)

const (
	PrivateNetworkName  = "sc-private"
	HomeVolumeName      = "sc-home"
	WorkspaceVolumeName = "sc-workspace"
	CAVolumeName        = "sc-ca"
	TailscaleName       = "sc-tailscale"
	DNSName             = "sc-dns"
)

type CreateRequest struct {
	Reference     string
	Domain        string
	OccupiedCIDRs []string
}

type CreatePlan struct {
	Reference             string            `json:"reference"`
	IncusProject          string            `json:"incusProject"`
	Domain                string            `json:"domain"`
	PrivateCIDR           string            `json:"privateCIDR"`
	PrivateNetwork        string            `json:"privateNetwork"`
	StoragePool           string            `json:"storagePool"`
	HomeVolume            string            `json:"homeVolume"`
	WorkspaceVolume       string            `json:"workspaceVolume"`
	CAVolume              string            `json:"caVolume"`
	TailscaleInstance     string            `json:"tailscaleInstance"`
	TailscaleAddress      string            `json:"tailscaleAddress"`
	DNSInstance           string            `json:"dnsInstance"`
	DNSAddress            string            `json:"dnsAddress"`
	DefaultTemplate       string            `json:"defaultTemplate"`
	Sidecars              []SidecarPlan     `json:"sidecars"`
	ProjectMetadataConfig map[string]string `json:"projectMetadataConfig"`
}

type Creator interface {
	CreateProject(context.Context, CreatePlan) error
}

type SidecarPlan struct {
	Name       string            `json:"name"`
	Role       string            `json:"role"`
	Address    string            `json:"address"`
	ImageAlias string            `json:"imageAlias"`
	Config     map[string]string `json:"config"`
	Devices    map[string]Device `json:"devices"`
	Start      bool              `json:"start"`
}

type Device map[string]string

func PlanCreate(admin config.Admin, request CreateRequest) (CreatePlan, error) {
	if err := admin.Validate(); err != nil {
		return CreatePlan{}, err
	}
	ref, err := naming.ParseProjectRef(request.Reference)
	if err != nil {
		return CreatePlan{}, err
	}
	domain, err := normalizeDomain(request.Domain)
	if err != nil {
		return CreatePlan{}, err
	}
	incusName, err := naming.IncusProjectNameWithPrefix(admin.ProjectPrefix, ref)
	if err != nil {
		return CreatePlan{}, err
	}
	projectCIDR, err := cidr.Allocate(admin.CIDRPool, cidr.DefaultProjectPrefixBits, request.OccupiedCIDRs)
	if err != nil {
		return CreatePlan{}, err
	}
	tailscaleAddress, err := roleAddress(projectCIDR, 2)
	if err != nil {
		return CreatePlan{}, err
	}
	dnsAddress, err := roleAddress(projectCIDR, 53)
	if err != nil {
		return CreatePlan{}, err
	}

	projectMetadata := meta.Project{
		Owner:           ref.Owner,
		Project:         ref.Project,
		Domain:          domain,
		PrivateCIDR:     projectCIDR.String(),
		DefaultTemplate: "ai",
	}
	metadataConfig, err := meta.ProjectConfig(projectMetadata)
	if err != nil {
		return CreatePlan{}, err
	}

	return CreatePlan{
		Reference:         ref.String(),
		IncusProject:      incusName,
		Domain:            domain,
		PrivateCIDR:       projectCIDR.String(),
		PrivateNetwork:    PrivateNetworkName,
		StoragePool:       admin.StoragePool,
		HomeVolume:        HomeVolumeName,
		WorkspaceVolume:   WorkspaceVolumeName,
		CAVolume:          CAVolumeName,
		TailscaleInstance: TailscaleName,
		TailscaleAddress:  tailscaleAddress.String(),
		DNSInstance:       DNSName,
		DNSAddress:        dnsAddress.String(),
		DefaultTemplate:   projectMetadata.DefaultTemplate,
		Sidecars: []SidecarPlan{
			sidecarPlan(ref, admin, TailscaleName, "tailscale", tailscaleAddress.String()),
			sidecarPlan(ref, admin, DNSName, "dns", dnsAddress.String()),
		},
		ProjectMetadataConfig: metadataConfig,
	}, nil
}

func sidecarPlan(ref naming.ProjectRef, admin config.Admin, name string, role string, address string) SidecarPlan {
	return SidecarPlan{
		Name:       name,
		Role:       role,
		Address:    address,
		ImageAlias: admin.Images.Base,
		Config: map[string]string{
			meta.KeyKind:    "sidecar",
			meta.KeyOwner:   ref.Owner,
			meta.KeyProject: ref.Project,
			meta.KeyName:    name,
			meta.KeyVersion: "1",
		},
		Devices: map[string]Device{
			"root": {
				"type": "disk",
				"pool": admin.StoragePool,
				"path": "/",
			},
			"eth0": {
				"type":         "nic",
				"nictype":      "bridged",
				"parent":       PrivateNetworkName,
				"ipv4.address": address,
			},
		},
		Start: true,
	}
}

func normalizeDomain(value string) (string, error) {
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if domain == "" {
		return "", fmt.Errorf("domain is required")
	}
	if strings.Contains(domain, "/") || strings.Contains(domain, " ") {
		return "", fmt.Errorf("invalid project domain %q", value)
	}
	return domain, nil
}

func roleAddress(prefix netip.Prefix, hostOctet byte) (netip.Addr, error) {
	addr, err := cidr.RoleAddress(prefix, hostOctet)
	if err != nil {
		return netip.Addr{}, err
	}
	return addr, nil
}
