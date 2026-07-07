package tenant

import (
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/certs"
	"github.com/thieso2/sandcastle-incus/internal/cidr"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	domainrules "github.com/thieso2/sandcastle-incus/internal/domain"
	"github.com/thieso2/sandcastle-incus/internal/naming"
)

// V2DefaultProfileUserData renders the cloud-init user-data baked into a v2
// project's default profile. Freeform `incus launch` of a cloud-init image
// applies it at first boot, creating the login user (UID 2000, sudo) with the
// tenant's SSH key and an enabled sshd — so machines are reachable over the
// tenant's tailnet with no Sandcastle-in-the-loop configure step.
//
// When project and suffix are known the user-data is a jinja template that
// stamps each machine with its canonical Machine Private Hostname
// <machine>.<project>.<suffix> (ADR-0018) — identity only; resolution comes
// from the sidecar CoreDNS zone.
func V2DefaultProfileUserData(user string, sshKey string, project string, suffix string) string {
	header := "#cloud-config\n"
	identity := ""
	project = strings.TrimSpace(project)
	suffix = strings.TrimSpace(suffix)
	if project != "" && suffix != "" {
		header = "## template: jinja\n#cloud-config\n"
		identity = fmt.Sprintf("fqdn: {{ v1.local_hostname }}.%s.%s\nprefer_fqdn_over_hostname: true\n", project, suffix)
	}
	return header + identity + fmt.Sprintf(`users:
  - name: %s
    uid: 2000
    groups: [sudo]
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    ssh_authorized_keys:
      - %s
packages:
  - openssh-server
runcmd:
  - [systemctl, enable, --now, ssh]
`, user, sshKey)
}

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
	requestedSuffix := strings.TrimSpace(request.DNSSuffix)
	existingSuffix := strings.TrimSpace(request.ExistingDNSSuffix)
	if requestedSuffix != "" && existingSuffix != "" && requestedSuffix != existingSuffix {
		return CreatePlanV2{}, TerminalProvisionError{Err: fmt.Errorf("the Tenant DNS Suffix is immutable: tenant %s already uses %q (requested %q)", ref.Tenant, existingSuffix, requestedSuffix)}
	}
	effectiveSuffix := requestedSuffix
	if effectiveSuffix == "" {
		effectiveSuffix = existingSuffix
	}
	if effectiveSuffix == "" {
		effectiveSuffix = ref.Tenant
	}
	suffix, err := domainrules.ValidateTenantDNSSuffix(effectiveSuffix, domainrules.Policy{
		AllowedSuffixes: admin.AllowedDomainSuffixes,
		DeniedSuffixes:  admin.DeniedDomainSuffixes,
	})
	if err != nil {
		// Bad user input — no retry can fix a rejected suffix.
		return CreatePlanV2{}, TerminalProvisionError{Err: err}
	}
	var tenantCIDR netip.Prefix
	if pref := strings.TrimSpace(request.PreferredCIDR); pref != "" {
		// Reuse the tenant's existing /24 (idempotent re-provision).
		tenantCIDR, err = netip.ParsePrefix(pref)
		if err != nil {
			return CreatePlanV2{}, fmt.Errorf("parse preferred CIDR %q: %w", pref, err)
		}
		tenantCIDR = tenantCIDR.Masked()
		// A reused CIDR must still come from this install's pool: anything
		// else means the reuse scan picked up a foreign install's tenant (or
		// the pool changed), and provisioning it would stand up a bridge on
		// address space this install doesn't own.
		if pool, perr := netip.ParsePrefix(strings.TrimSpace(admin.CIDRPool)); perr == nil {
			if !pool.Contains(tenantCIDR.Addr()) || tenantCIDR.Bits() < pool.Bits() {
				return CreatePlanV2{}, fmt.Errorf("preferred CIDR %s is outside the tenant CIDR pool %s", tenantCIDR, pool)
			}
		}
	} else if tenantCIDR, err = cidr.Allocate(admin.CIDRPool, cidr.DefaultTenantPrefixBits, request.OccupiedCIDRs); err != nil {
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
	dnsFiles, err := dns.RenderInitial(suffix, dnsAddress.String())
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
		StoragePool:        admin.StoragePool,
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
