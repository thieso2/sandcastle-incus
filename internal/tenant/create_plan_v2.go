package tenant

import (
	"strings"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/certs"
	"github.com/thieso2/sandcastle-incus/internal/cidr"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	domainrules "github.com/thieso2/sandcastle-incus/internal/domain"
	"github.com/thieso2/sandcastle-incus/internal/naming"
)

// DefaultV2UnixUser is the login user baked into a v2 project's default
// profile when the create request does not specify one. It matches the UID-2000
// convention (ADR-0014) applied when the profile is materialized.
const DefaultV2UnixUser = "dev"

// CreatePlanV2 describes the v2 MVP tenant bring-up (ADR-0016): one per-tenant
// infra project holding a single sidecar (CoreDNS + Tailscale + Caddy), one
// shared per-tenant bridge, and a seeded default app project. Machines are
// created later by the tenant with native incus into app projects on the shared
// bridge; DNS is flat (<machine>.<suffix>).
type CreatePlanV2 struct {
	Tenant             string     `json:"tenant"`
	Prefix             string     `json:"prefix"`
	InfraProject       string     `json:"infraProject"`
	DefaultProject     string     `json:"defaultProject"`
	Bridge             string     `json:"bridge"`
	DNSSuffix          string     `json:"dnsSuffix"`
	PrivateCIDR        string     `json:"privateCIDR"`
	GatewayAddress     string     `json:"gatewayAddress"`
	TailscaleAddress   string     `json:"tailscaleAddress"`
	DNSAddress         string     `json:"dnsAddress"`
	StoragePool        string     `json:"storagePool"`
	HomeVolume         string     `json:"homeVolume"`
	WorkspaceVolume    string     `json:"workspaceVolume"`
	CAVolume           string     `json:"caVolume"`
	SidecarInstance    string     `json:"sidecarInstance"`
	SidecarImage       string     `json:"sidecarImage"`
	DefaultProfileUser string     `json:"defaultProfileUser"`
	SSHPublicKey       string     `json:"sshPublicKey"`
	ImageAliases       []string   `json:"imageAliases"`
	DNSFiles           []dns.File `json:"dnsFiles"`
	TenantCA           TenantCA   `json:"tenantCA"`
	RestrictedProjects []string   `json:"restrictedProjects"`
}

// PlanCreateV2 builds a CreatePlanV2 from admin config and a create request.
// It is pure (aside from CA key generation and the current time) so it can be
// unit tested without touching Incus.
func PlanCreateV2(admin config.Admin, request CreateRequest) (CreatePlanV2, error) {
	if err := admin.Validate(); err != nil {
		return CreatePlanV2{}, err
	}
	ref, err := naming.ParseTenantRef(request.Reference)
	if err != nil {
		return CreatePlanV2{}, err
	}
	prefix := strings.TrimSpace(admin.IncusProjectPrefix)
	if prefix == "" || prefix == naming.DefaultIncusProjectPrefix {
		prefix = naming.V2IncusProjectPrefix
	}
	infraProject, err := naming.V2TenantInfraProjectName(prefix, ref.Tenant)
	if err != nil {
		return CreatePlanV2{}, err
	}
	defaultProject, err := naming.V2ProjectName(prefix, ref.Tenant, naming.DefaultProjectName)
	if err != nil {
		return CreatePlanV2{}, err
	}
	bridge, err := naming.V2BridgeName(prefix, ref.Tenant)
	if err != nil {
		return CreatePlanV2{}, err
	}
	unixUser := strings.TrimSpace(request.UnixUser)
	if unixUser == "" {
		unixUser = DefaultV2UnixUser
	}
	if err := naming.ValidateUnixUsername(unixUser); err != nil {
		return CreatePlanV2{}, err
	}
	suffix, err := domainrules.ValidateTenantDNSSuffix(ref.Tenant, domainrules.Policy{
		AllowedSuffixes: admin.AllowedDomainSuffixes,
		DeniedSuffixes:  admin.DeniedDomainSuffixes,
	})
	if err != nil {
		return CreatePlanV2{}, err
	}
	tenantCIDR, err := cidr.Allocate(admin.CIDRPool, cidr.DefaultTenantPrefixBits, request.OccupiedCIDRs)
	if err != nil {
		return CreatePlanV2{}, err
	}
	gatewayAddress, err := roleAddress(tenantCIDR, cidr.GatewayHostOctet)
	if err != nil {
		return CreatePlanV2{}, err
	}
	tailscaleAddress, err := roleAddress(tenantCIDR, cidr.TailscaleHostOctet)
	if err != nil {
		return CreatePlanV2{}, err
	}
	dnsAddress, err := roleAddress(tenantCIDR, cidr.DNSHostOctet)
	if err != nil {
		return CreatePlanV2{}, err
	}
	dnsFiles, err := dns.RenderInitial(suffix, dnsAddress.String(), gatewayAddress.String())
	if err != nil {
		return CreatePlanV2{}, err
	}
	ca, err := certs.GenerateCA("Sandcastle "+ref.String()+" tenant CA", time.Now().UTC())
	if err != nil {
		return CreatePlanV2{}, err
	}

	return CreatePlanV2{
		Tenant:             ref.Tenant,
		Prefix:             prefix,
		InfraProject:       infraProject,
		DefaultProject:     defaultProject,
		Bridge:             bridge,
		DNSSuffix:          suffix,
		PrivateCIDR:        tenantCIDR.String(),
		GatewayAddress:     gatewayAddress.String(),
		TailscaleAddress:   tailscaleAddress.String(),
		DNSAddress:         dnsAddress.String(),
		StoragePool:        infraProject,
		HomeVolume:         HomeVolumeName,
		WorkspaceVolume:    WorkspaceVolumeName,
		CAVolume:           CAVolumeName,
		SidecarInstance:    infraProject,
		SidecarImage:       admin.Images.Base,
		DefaultProfileUser: unixUser,
		SSHPublicKey:       request.SSHPublicKey,
		ImageAliases:       uniqueImageAliases(admin.Images.Base, admin.Images.AI),
		DNSFiles:           dnsFiles,
		TenantCA: TenantCA{
			CertificatePath: TenantCACertPath,
			PrivateKeyPath:  TenantCAKeyPath,
			CertificatePEM:  ca.CertificatePEM,
			PrivateKeyPEM:   ca.PrivateKeyPEM,
		},
		RestrictedProjects: []string{defaultProject},
	}, nil
}
