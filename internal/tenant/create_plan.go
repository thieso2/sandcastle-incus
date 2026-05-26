package tenant

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"net/netip"
	"strings"
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
	TenantCACertPath    = "/ca.crt"
	TenantCAKeyPath     = "/ca.key"
	DNSName             = "sc-dns"
)

// TailscaleInstanceName returns the Incus instance name for the tenant's Tailscale sidecar.
// It uses the Incus project name so the host appears with the tenant identity in the Tailnet.
func TailscaleInstanceName(incusProjectName string) string {
	return incusProjectName
}

// PrivateNetworkName returns the bridge network name for a tenant. Bridge networks are subject to
// Linux's 15-char IFNAMSIZ-1 limit, so long project names use a stable hashed name instead of a
// truncation that can collide across tenants.
func PrivateNetworkName(incusProjectName string) string {
	if len(incusProjectName) <= 15 {
		return incusProjectName
	}
	sum := sha1.Sum([]byte(incusProjectName))
	return "sc-" + hex.EncodeToString(sum[:])[:12]
}

type CreateRequest struct {
	Reference     string
	SSHPublicKey  string
	OccupiedCIDRs []string
	Personal      bool
	CreatedBy     string
	UnixUser      string
}

type CreatePlan struct {
	Reference            string            `json:"reference"`
	IncusProject         string            `json:"incusProject"`
	InfraProject         string            `json:"infraProject"`
	NativeProject        string            `json:"nativeProject"`
	DNSSuffix            string            `json:"dnsSuffix"`
	PrivateCIDR          string            `json:"privateCIDR"`
	PrivateNetwork       string            `json:"privateNetwork"`
	StoragePool          string            `json:"storagePool"`
	AdminStoragePool     string            `json:"adminStoragePool"`
	HomeVolume           string            `json:"homeVolume"`
	WorkspaceVolume      string            `json:"workspaceVolume"`
	CAVolume             string            `json:"caVolume"`
	TailscaleInstance    string            `json:"tailscaleInstance"`
	TailscaleAddress     string            `json:"tailscaleAddress"`
	DNSInstance          string            `json:"dnsInstance"`
	DNSAddress           string            `json:"dnsAddress"`
	DefaultTemplate      string            `json:"defaultTemplate"`
	ImageAliases         []string          `json:"imageAliases"`
	InfraImageAliases    []string          `json:"infraImageAliases"`
	Sidecars             []SidecarPlan     `json:"sidecars"`
	DNSFiles             []dns.File        `json:"dnsFiles"`
	TenantCA             TenantCA          `json:"tenantCA"`
	TenantMetadataConfig map[string]string `json:"tenantMetadataConfig"`
}

type Creator interface {
	CreateTenant(context.Context, CreatePlan) error
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

type TenantCA struct {
	CertificatePath string `json:"certificatePath"`
	PrivateKeyPath  string `json:"privateKeyPath"`
	CertificatePEM  []byte `json:"-"`
	PrivateKeyPEM   []byte `json:"-"`
}

func PlanCreate(admin config.Admin, request CreateRequest) (CreatePlan, error) {
	if err := admin.Validate(); err != nil {
		return CreatePlan{}, err
	}
	ref, incusName, err := createTenantIdentity(admin, request)
	if err != nil {
		return CreatePlan{}, err
	}
	unixUser := strings.TrimSpace(request.UnixUser)
	if unixUser != "" {
		if err := naming.ValidateUnixUsername(unixUser); err != nil {
			return CreatePlan{}, err
		}
	}
	tenantSuffix, err := domainrules.ValidateTenantDNSSuffix(ref.Tenant, domainrules.Policy{
		AllowedSuffixes: admin.AllowedDomainSuffixes,
		DeniedSuffixes:  admin.DeniedDomainSuffixes,
	})
	if err != nil {
		return CreatePlan{}, err
	}
	tenantCIDR, err := cidr.Allocate(admin.CIDRPool, cidr.DefaultTenantPrefixBits, request.OccupiedCIDRs)
	if err != nil {
		return CreatePlan{}, err
	}
	tailscaleAddress, err := roleAddress(tenantCIDR, cidr.TailscaleHostOctet)
	if err != nil {
		return CreatePlan{}, err
	}
	dnsAddress, err := roleAddress(tenantCIDR, cidr.DNSHostOctet)
	if err != nil {
		return CreatePlan{}, err
	}

	tenantMetadata := meta.Tenant{
		Tenant:       ref.Tenant,
		Personal:     request.Personal,
		CreatedBy:    request.CreatedBy,
		UnixUser:     unixUser,
		PrivateCIDR:  tenantCIDR.String(),
		Projects:     []meta.Project{{Name: naming.DefaultProjectName}},
		SSHPublicKey: request.SSHPublicKey,
		Tailscale: meta.Tailscale{
			State: meta.TailscaleStateRunningLoggedOut,
		},
	}
	metadataConfig, err := meta.TenantConfig(tenantMetadata)
	if err != nil {
		return CreatePlan{}, err
	}
	dnsFiles, err := dns.RenderInitial(tenantSuffix, dnsAddress.String())
	if err != nil {
		return CreatePlan{}, err
	}
	ca, err := certs.GenerateCA("Sandcastle "+ref.String()+" tenant CA", time.Now().UTC())
	if err != nil {
		return CreatePlan{}, err
	}

	return CreatePlan{
		Reference:         ref.String(),
		IncusProject:      incusName,
		InfraProject:      naming.TenantInfraIncusProjectName(incusName),
		NativeProject:     naming.TenantNativeIncusProjectName(incusName),
		DNSSuffix:         tenantSuffix,
		PrivateCIDR:       tenantCIDR.String(),
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
		DefaultTemplate:   "ai",
		ImageAliases:      uniqueImageAliases(admin.Images.Base, admin.Images.AI),
		InfraImageAliases: uniqueImageAliases(admin.Images.Base),
		Sidecars: []SidecarPlan{
			sidecarPlan(ref, admin, incusName, TailscaleInstanceName(incusName), "tailscale", tailscaleAddress.String()),
			sidecarPlan(ref, admin, incusName, DNSName, "dns", dnsAddress.String()),
		},
		DNSFiles: dnsFiles,
		TenantCA: TenantCA{
			CertificatePath: TenantCACertPath,
			PrivateKeyPath:  TenantCAKeyPath,
			CertificatePEM:  ca.CertificatePEM,
			PrivateKeyPEM:   ca.PrivateKeyPEM,
		},
		TenantMetadataConfig: metadataConfig,
	}, nil
}

func createTenantIdentity(admin config.Admin, request CreateRequest) (naming.TenantRef, string, error) {
	if request.Personal {
		tenantName := strings.ToLower(strings.TrimSpace(request.Reference))
		if err := naming.ValidateGitHubUsernameTenantName(tenantName); err != nil {
			return naming.TenantRef{}, "", err
		}
		incusName, err := naming.PersonalTenantIncusProjectNameWithPrefix(admin.IncusProjectPrefix, tenantName)
		if err != nil {
			return naming.TenantRef{}, "", err
		}
		return naming.TenantRef{Tenant: tenantName}, incusName, nil
	}
	ref, err := naming.ParseTenantRef(request.Reference)
	if err != nil {
		return naming.TenantRef{}, "", err
	}
	incusName, err := naming.TenantIncusProjectNameWithPrefix(admin.IncusProjectPrefix, ref)
	if err != nil {
		return naming.TenantRef{}, "", err
	}
	return ref, incusName, nil
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

func NewSidecarPlan(ref naming.TenantRef, admin config.Admin, incusName string, name string, role string, address string) SidecarPlan {
	return sidecarPlan(ref, admin, incusName, name, role, address)
}

func UniqueImageAliases(aliases ...string) []string {
	return uniqueImageAliases(aliases...)
}

func sidecarPlan(ref naming.TenantRef, admin config.Admin, incusName string, name string, role string, address string) SidecarPlan {
	return SidecarPlan{
		Name:       name,
		Role:       role,
		Address:    address,
		ImageAlias: admin.Images.Base,
		Config: map[string]string{
			meta.KeyKind:    "sidecar",
			meta.KeyTenant:  ref.Tenant,
			meta.KeyMachine: name,
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
