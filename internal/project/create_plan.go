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
	HomeVolumeName      = "sc-home"
	WorkspaceVolumeName = "sc-workspace"
	CAVolumeName        = "sc-ca"
	ProjectCACertPath   = "/ca.crt"
	ProjectCAKeyPath    = "/ca.key"
	DNSName             = "sc-dns"
)

// TailscaleInstanceName returns the Incus instance name for the project's Tailscale sidecar.
// It uses the Incus project name so the host appears with the project identity in the Tailnet.
func TailscaleInstanceName(incusProjectName string) string {
	return incusProjectName
}

// PrivateNetworkName returns the bridge network name for a project. Bridge networks live in the
// default Incus namespace and are subject to Linux's 15-char IFNAMSIZ-1 limit, so names longer
// than 15 chars are truncated.
func PrivateNetworkName(incusProjectName string) string {
	if len(incusProjectName) <= 15 {
		return incusProjectName
	}
	return incusProjectName[:15]
}

type CreateRequest struct {
	Reference     string
	Domain        string
	SSHPublicKey  string
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
	AdminStoragePool      string            `json:"adminStoragePool"`
	HomeVolume            string            `json:"homeVolume"`
	WorkspaceVolume       string            `json:"workspaceVolume"`
	CAVolume              string            `json:"caVolume"`
	TailscaleInstance     string            `json:"tailscaleInstance"`
	TailscaleAddress      string            `json:"tailscaleAddress"`
	DNSInstance           string            `json:"dnsInstance"`
	DNSAddress            string            `json:"dnsAddress"`
	DefaultTemplate       string            `json:"defaultTemplate"`
	ImageAliases          []string          `json:"imageAliases"`
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
		SSHPublicKey:    request.SSHPublicKey,
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
		PrivateNetwork:    PrivateNetworkName(incusName),
		StoragePool:       incusName,
		AdminStoragePool:  admin.StoragePool,
		HomeVolume:        HomeVolumeName,
		WorkspaceVolume:   WorkspaceVolumeName,
		CAVolume:          CAVolumeName,
		TailscaleInstance: TailscaleInstanceName(incusName),
		TailscaleAddress:  tailscaleAddress.String(),
		DNSInstance:       DNSName,
		DNSAddress:        dnsAddress.String(),
		DefaultTemplate:   projectMetadata.DefaultTemplate,
		ImageAliases:      uniqueImageAliases(admin.Images.Base, admin.Images.AI),
		Sidecars: []SidecarPlan{
			sidecarPlan(ref, admin, incusName, TailscaleInstanceName(incusName), "tailscale", tailscaleAddress.String()),
			sidecarPlan(ref, admin, incusName, DNSName, "dns", dnsAddress.String()),
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

func uniqueImageAliases(aliases ...string) []string {
	output := make([]string, 0, len(aliases))
	seen := map[string]bool{}
	for _, alias := range aliases {
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		output = append(output, alias)
	}
	return output
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

func sidecarPlan(ref naming.ProjectRef, admin config.Admin, incusName string, name string, role string, address string) SidecarPlan {
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
		Devices: sidecarDevices(incusName, incusName, role, address),
		Start:   true,
	}
}

func sidecarDevices(storagePool string, incusName string, role string, address string) map[string]Device {
	devices := map[string]Device{
		"root": {
			"type": "disk",
			"pool": storagePool,
			"path": "/",
		},
		"eth0": {
			"type":         "nic",
			"nictype":      "bridged",
			"parent":       PrivateNetworkName(incusName),
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
