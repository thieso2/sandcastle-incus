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
	UserKey           string
	Tenant            string
	IncusProject      string
	AccessibleTenants []string
	Token             string
	RemoteName        string
	Projects          []string
	Message           string
}

type TrustTokenCreator interface {
	CreateToken(context.Context, usertrust.UserPlan) (usertrust.TokenResult, error)
}

type Provisioner struct {
	Admin         config.Admin
	Tenants       tenant.IncusTenantStore
	TenantCreator tenant.Creator
	Trust         TrustTokenCreator
}

func (p Provisioner) EnsurePersonalTenant(ctx context.Context, user User) (PersonalTenantResult, error) {
	userKey := NormalizeGitHubUsername(user.UserKey)
	if userKey == "" {
		userKey = NormalizeGitHubUsername(user.GitHubUsernameNormalized)
	}
	if err := naming.ValidateGitHubUsernameTenantName(userKey); err != nil {
		return PersonalTenantResult{}, err
	}
	if p.Tenants == nil {
		return PersonalTenantResult{}, fmt.Errorf("tenant store is not configured")
	}
	if p.TenantCreator == nil {
		return PersonalTenantResult{}, fmt.Errorf("tenant creator is not configured")
	}
	if p.Trust == nil {
		return PersonalTenantResult{}, fmt.Errorf("trust manager is not configured")
	}
	summaries, err := tenant.List(ctx, p.Tenants)
	if err != nil {
		return PersonalTenantResult{}, err
	}
	var existing tenant.Summary
	for _, summary := range summaries {
		if summary.Tenant == userKey {
			existing = summary
			break
		}
	}
	incusProject := existing.IncusName
	if incusProject == "" {
		plan, err := tenant.PlanCreate(p.Admin, tenant.CreateRequest{
			Reference:     userKey,
			OccupiedCIDRs: tenant.OccupiedCIDRs(summaries),
			Personal:      true,
			CreatedBy:     userKey,
		})
		if err != nil {
			return PersonalTenantResult{}, err
		}
		if err := p.TenantCreator.CreateTenant(ctx, plan); err != nil {
			return PersonalTenantResult{}, err
		}
		incusProject = plan.IncusProject
	}
	tokenPlan, err := usertrust.PlanGrant(p.Admin, usertrust.GrantRequest{
		User:     userKey,
		Projects: []string{userKey},
		Personal: true,
	})
	if err != nil {
		return PersonalTenantResult{}, err
	}
	token, err := p.Trust.CreateToken(ctx, tokenPlan)
	if err != nil {
		return PersonalTenantResult{}, err
	}
	return PersonalTenantResult{
		UserKey:           userKey,
		Tenant:            userKey,
		IncusProject:      incusProject,
		AccessibleTenants: []string{userKey},
		Token:             token.Token,
		RemoteName:        token.RemoteName,
		Projects:          append([]string{}, token.Projects...),
		Message:           "Personal tenant " + userKey + " is ready.",
	}, nil
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
