package project

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/certs"
	"github.com/thieso2/sandcastle-incus/internal/cidr"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	domainrules "github.com/thieso2/sandcastle-incus/internal/domain"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
)

const (
	PrivateNetworkName  = "sc-private"
	HomeVolumeName      = "sc-home"
	WorkspaceVolumeName = "sc-workspace"
	CAVolumeName        = "sc-ca"
	ProjectCACertPath   = "/ca.crt"
	ProjectCAKeyPath    = "/ca.key"
	TailscaleName       = "sc-tailscale"
	DNSName             = "sc-dns"
)

type CreateRequest struct {
	Reference     string
	Domain        string
	OccupiedCIDRs []string
	DomainClaims  []DomainClaim
}

type DomainClaim struct {
	Owner   string
	Project string
	Domain  string
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
	DNSFiles              []dns.File        `json:"dnsFiles"`
	ProjectCA             ProjectCA         `json:"projectCA"`
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

type ProjectCA struct {
	CertificatePath string `json:"certificatePath"`
	PrivateKeyPath  string `json:"privateKeyPath"`
	CertificatePEM  []byte `json:"-"`
	PrivateKeyPEM   []byte `json:"-"`
}

func PlanCreate(admin config.Admin, request CreateRequest) (CreatePlan, error) {
	if err := admin.Validate(); err != nil {
		return CreatePlan{}, err
	}
	ref, err := naming.ParseProjectRef(request.Reference)
	if err != nil {
		return CreatePlan{}, err
	}
	projectDomain, err := domainrules.ValidateProjectDomain(request.Domain, domainrules.Policy{
		AllowedSuffixes: admin.AllowedDomainSuffixes,
		DeniedSuffixes:  admin.DeniedDomainSuffixes,
	})
	if err != nil {
		return CreatePlan{}, err
	}
	if err := validateDomainClaim(ref, projectDomain, request.DomainClaims); err != nil {
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
		Domain:          projectDomain,
		PrivateCIDR:     projectCIDR.String(),
		DefaultTemplate: "ai",
		Tailscale: meta.Tailscale{
			State: meta.TailscaleStateRunningLoggedOut,
		},
	}
	metadataConfig, err := meta.ProjectConfig(projectMetadata)
	if err != nil {
		return CreatePlan{}, err
	}
	dnsFiles, err := dns.RenderInitial(projectDomain, dnsAddress.String())
	if err != nil {
		return CreatePlan{}, err
	}
	ca, err := certs.GenerateCA("Sandcastle "+ref.String()+" project CA", time.Now().UTC())
	if err != nil {
		return CreatePlan{}, err
	}

	return CreatePlan{
		Reference:         ref.String(),
		IncusProject:      incusName,
		Domain:            projectDomain,
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
		DNSFiles: dnsFiles,
		ProjectCA: ProjectCA{
			CertificatePath: ProjectCACertPath,
			PrivateKeyPath:  ProjectCAKeyPath,
			CertificatePEM:  ca.CertificatePEM,
			PrivateKeyPEM:   ca.PrivateKeyPEM,
		},
		ProjectMetadataConfig: metadataConfig,
	}, nil
}

func DomainClaims(projects []Summary) []DomainClaim {
	claims := make([]DomainClaim, 0, len(projects))
	for _, project := range projects {
		if project.Domain == "" {
			continue
		}
		claims = append(claims, DomainClaim{
			Owner:   project.Owner,
			Project: project.Name,
			Domain:  project.Domain,
		})
	}
	return claims
}

func validateDomainClaim(ref naming.ProjectRef, domain string, claims []DomainClaim) error {
	for _, claim := range claims {
		if claim.Owner != ref.Owner || claim.Domain != domain {
			continue
		}
		if claim.Project == ref.Project {
			continue
		}
		return fmt.Errorf("project domain %q is already used by %s/%s", domain, claim.Owner, claim.Project)
	}
	return nil
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
		Devices: sidecarDevices(admin, role, address),
		Start:   true,
	}
}

func sidecarDevices(admin config.Admin, role string, address string) map[string]Device {
	devices := map[string]Device{
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
	}
	if role == "tailscale" {
		devices["tun"] = Device{
			"type": "unix-char",
			"path": "/dev/net/tun",
		}
	}
	return devices
}

func roleAddress(prefix netip.Prefix, hostOctet byte) (netip.Addr, error) {
	addr, err := cidr.RoleAddress(prefix, hostOctet)
	if err != nil {
		return netip.Addr{}, err
	}
	return addr, nil
}
