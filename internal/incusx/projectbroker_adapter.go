package incusx

import (
	"context"
	"fmt"

	"github.com/lxc/incus/v6/shared/api"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/projectbroker"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

// ProjectBrokerCreator implements projectbroker.ProjectCreator: it performs the
// privileged v2 project scaffolding (CreateProjectV2) and extends the tenant's
// restricted certificate to include the new project so the tenant's native
// incus client can immediately use it.
type ProjectBrokerCreator struct {
	Creator TenantCreator
	Trust   usertrust.Manager
	// Prefix is the installation prefix this broker/auth-app serves; it scopes
	// the infra-project lookup and the certificate/remote names so several
	// sandcastles can share one Incus host.
	Prefix string
}

func (p ProjectBrokerCreator) CreateTenantProject(ctx context.Context, tenant string, project string) (projectbroker.ProjectResult, error) {
	res, err := p.Creator.CreateProjectV2(ctx, p.Prefix, tenant, project)
	if err != nil {
		return projectbroker.ProjectResult{}, err
	}
	if p.Trust != nil {
		if err := p.Trust.Grant(ctx, usertrust.UserPlan{
			User:            tenant,
			CertificateName: usertrust.RestrictedInstallName(p.Prefix, tenant),
			RemoteName:      usertrust.RemoteInstallName(p.Prefix, tenant),
			Restricted:      true,
			Projects:        []string{res.IncusProject},
			Description:     "Sandcastle v2 tenant " + tenant,
		}); err != nil {
			return projectbroker.ProjectResult{}, fmt.Errorf("extend tenant certificate for %s: %w", res.IncusProject, err)
		}
	}
	return projectbroker.ProjectResult{
		Tenant:       res.Tenant,
		Project:      res.Project,
		IncusProject: res.IncusProject,
		Bridge:       res.Bridge,
		DNSSuffix:    res.DNSSuffix,
	}, nil
}

// TenantProvisionerAdapter implements projectbroker.TenantProvisioner: the
// broker's admin plane drives the full v2 tenant bring-up + mints the
// enrollment token, using the admin Incus credentials the broker holds.
type TenantProvisionerAdapter struct {
	Creator      TenantCreator
	Trust        usertrust.Manager
	Admin        config.Admin
	SidecarImage string
	// Tenants lists existing tenants so the CIDR allocator skips /24s already in
	// use; without it the allocator always returns the pool's first /24 and every
	// tenant after the first collides on its bridge gateway.
	Tenants tenant.IncusTenantStore
}

func (a TenantProvisionerAdapter) CreateTenant(ctx context.Context, req projectbroker.TenantRequest) (projectbroker.TenantResult, error) {
	var ownCIDR, ownSuffix string
	var occupied []string
	if a.Tenants != nil {
		var err error
		ownCIDR, ownSuffix, occupied, err = tenant.ProvisionReuseInputs(ctx, a.Tenants, a.Admin.IncusProjectPrefix, req.Tenant)
		if err != nil {
			return projectbroker.TenantResult{}, fmt.Errorf("list allocated CIDRs: %w", err)
		}
	}
	plan, err := tenant.PlanCreateV2(a.Admin, tenant.CreateRequest{
		Reference:         req.Tenant,
		SSHPublicKey:      req.SSHPublicKey,
		OccupiedCIDRs:     occupied,
		PreferredCIDR:     ownCIDR,
		DNSSuffix:         req.DNSSuffix,
		ExistingDNSSuffix: ownSuffix,
	})
	if err != nil {
		return projectbroker.TenantResult{}, err
	}
	var tailscaleLoginURL string
	if err := a.Creator.CreateTenantV2(ctx, plan, CreateV2Options{
		TailscaleAuthKey:    req.TailscaleAuthKey,
		SidecarImage:        a.SidecarImage,
		OnTailscaleLoginURL: func(u string) { tailscaleLoginURL = u },
	}); err != nil {
		return projectbroker.TenantResult{}, err
	}
	result := projectbroker.TenantResult{
		Tenant:            plan.Tenant,
		InfraProject:      plan.InfraProject,
		DefaultProject:    plan.DefaultProject,
		Bridge:            plan.Bridge,
		DNSSuffix:         plan.DNSSuffix,
		TailscaleLoginURL: tailscaleLoginURL,
	}
	if a.Trust != nil {
		tok, err := a.Trust.CreateToken(ctx, usertrust.UserPlan{
			User:            plan.Tenant,
			CertificateName: usertrust.RestrictedInstallName(plan.Prefix, plan.Tenant),
			RemoteName:      usertrust.RemoteInstallName(plan.Prefix, plan.Tenant),
			Restricted:      true,
			Projects:        plan.RestrictedProjects,
			Description:     "Sandcastle v2 tenant " + plan.Tenant,
		})
		if err != nil {
			return projectbroker.TenantResult{}, fmt.Errorf("mint enrollment token: %w", err)
		}
		result.Token = tok.Token
		result.RemoteName = tok.RemoteName
	}
	return result, nil
}

// AdminAuthorizer implements projectbroker.AdminAuthorizer by treating any
// trusted, unrestricted client certificate on the Incus server as an admin.
type AdminAuthorizer struct {
	Remote     string
	ConfigPath string
	Server     RouteBrokerTrustServer
}

func NewAdminAuthorizer(remote string) AdminAuthorizer {
	return AdminAuthorizer{Remote: remote}
}

func (a AdminAuthorizer) IsAdmin(ctx context.Context, fingerprint string) (bool, error) {
	server := a.Server
	if server == nil {
		loaded, err := LoadCLIConfig(a.ConfigPath)
		if err != nil {
			return false, fmt.Errorf("load Incus config: %w", err)
		}
		remote := a.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return false, fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = instanceServer
	}
	certificates, err := server.GetCertificates()
	if err != nil {
		return false, fmt.Errorf("list Incus certificates: %w", err)
	}
	want := normalizeFingerprint(fingerprint)
	for _, c := range certificates {
		if normalizeFingerprint(c.Fingerprint) != want {
			continue
		}
		return c.Type == api.CertificateTypeClient && !c.Restricted, nil
	}
	return false, nil
}
