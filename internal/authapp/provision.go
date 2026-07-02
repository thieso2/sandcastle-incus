package authapp

import (
	"context"
	"fmt"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

type PersonalTenantProvisioner interface {
	EnsurePersonalTenant(context.Context, User) (PersonalTenantResult, error)
}

type PersonalTenantResult struct {
	UserKey             string
	Tenant              string
	IncusProject        string
	AccessibleTenants   []string
	Token               string
	RemoteName          string
	Projects            []string
	CurrentProject      string
	DefaultProjectReady bool
	TenantTailnetReady  bool
	Message             string
}

type TrustTokenCreator interface {
	CreateToken(context.Context, usertrust.UserPlan) (usertrust.TokenResult, error)
}

type Provisioner struct {
	Admin           config.Admin
	Tenants         tenant.IncusTenantStore
	Trust           TrustTokenCreator
	DefaultUnixUser string

	// V2Create, when set, routes login provisioning through the v2 flow
	// (default project + sidecar) instead of the v1 Personal Tenant path.
	// The caller supplies the closure so this package need not import incusx.
	V2Create func(context.Context, tenant.CreatePlanV2) error
}

// ensurePersonalTenantV2 provisions (or re-ensures) the caller's v2 tenant via
// V2Create and mints a restricted enrollment token scoped to its default
// project. The SSH key is applied separately by the device flow after approval.
func (p Provisioner) ensurePersonalTenantV2(ctx context.Context, userKey string, sshPublicKey string) (PersonalTenantResult, error) {
	if p.Trust == nil {
		return PersonalTenantResult{}, fmt.Errorf("trust manager is not configured")
	}
	// Avoid CIDR collisions with existing tenants (the v2 pool is shared). Use
	// AllocatedCIDRs, not List+OccupiedCIDRs — List only surfaces v1 kind=tenant
	// projects, so it would miss every v2 tenant and let the allocator collide.
	var occupied []string
	if p.Tenants != nil {
		if cidrs, err := tenant.AllocatedCIDRs(ctx, p.Tenants); err == nil {
			occupied = cidrs
		}
	}
	plan, err := tenant.PlanCreateV2(p.Admin, tenant.CreateRequest{
		Reference:     userKey,
		SSHPublicKey:  strings.TrimSpace(sshPublicKey),
		OccupiedCIDRs: occupied,
	})
	if err != nil {
		return PersonalTenantResult{}, err
	}
	if err := p.V2Create(ctx, plan); err != nil {
		return PersonalTenantResult{}, err
	}
	tok, err := p.Trust.CreateToken(ctx, usertrust.UserPlan{
		User:            plan.Tenant,
		CertificateName: usertrust.RestrictedName(plan.Tenant),
		RemoteName:      usertrust.RestrictedName(plan.Tenant),
		Restricted:      true,
		Projects:        plan.RestrictedProjects,
		Description:     "Sandcastle v2 tenant " + plan.Tenant,
	})
	if err != nil {
		return PersonalTenantResult{}, err
	}
	return PersonalTenantResult{
		UserKey:             userKey,
		Tenant:              plan.Tenant,
		IncusProject:        plan.DefaultProject,
		AccessibleTenants:   []string{plan.Tenant},
		Token:               tok.Token,
		RemoteName:          tok.RemoteName,
		Projects:            append([]string{}, tok.Projects...),
		CurrentProject:      naming.DefaultProjectName,
		DefaultProjectReady: true,
		TenantTailnetReady:  true,
		Message:             "v2 tenant " + plan.Tenant + " is ready.",
	}, nil
}

func (p Provisioner) EnsurePersonalTenant(ctx context.Context, user User) (PersonalTenantResult, error) {
	userKey := NormalizeGitHubUsername(user.UserKey)
	if userKey == "" {
		userKey = NormalizeGitHubUsername(user.GitHubUsernameNormalized)
	}
	if err := naming.ValidateGitHubUsernameTenantName(userKey); err != nil {
		return PersonalTenantResult{}, err
	}
	if p.V2Create == nil {
		return PersonalTenantResult{}, fmt.Errorf("v2 provisioning is not configured")
	}
	return p.ensurePersonalTenantV2(ctx, userKey, user.SSHPublicKey)
}

func (r PersonalTenantResult) normalizedMessage() string {
	if strings.TrimSpace(r.Message) != "" {
		return r.Message
	}
	if r.Tenant != "" {
		return "Personal tenant " + r.Tenant + " is ready."
	}
	return "Personal tenant is ready."
}
