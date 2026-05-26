package authapp

import (
	"context"
	"fmt"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
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
	TenantCreator   tenant.Creator
	ProjectUpdater  tenant.ProjectUpdater
	Trust           TrustTokenCreator
	DefaultUnixUser string
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
	projects := existing.Projects
	if incusProject == "" {
		plan, err := tenant.PlanCreate(p.Admin, tenant.CreateRequest{
			Reference:     userKey,
			OccupiedCIDRs: tenant.OccupiedCIDRs(summaries),
			Personal:      true,
			CreatedBy:     userKey,
			UnixUser:      strings.TrimSpace(p.DefaultUnixUser),
		})
		if err != nil {
			return PersonalTenantResult{}, err
		}
		if err := p.TenantCreator.CreateTenant(ctx, plan); err != nil {
			return PersonalTenantResult{}, err
		}
		incusProject = plan.IncusProject
		metadata, err := tenantMetadataFromCreatePlan(plan)
		if err != nil {
			return PersonalTenantResult{}, err
		}
		projects = append([]meta.Project{}, metadata.Projects...)
	} else {
		updatedProjects, err := p.ensureDefaultProject(ctx, existing)
		if err != nil {
			return PersonalTenantResult{}, err
		}
		projects = updatedProjects
	}
	tokenPlan, err := usertrust.PlanGrant(p.Admin, usertrust.GrantRequest{
		User:     userKey,
		Projects: []string{userKey},
		Personal: true,
	})
	if err != nil {
		return PersonalTenantResult{}, err
	}
	// Grant access to infra and native projects so the user cert can manage DNS/Tailscale sidecars
	// and the freeform native project.
	tokenPlan.Projects = append(tokenPlan.Projects,
		naming.TenantInfraIncusProjectName(incusProject),
		naming.TenantNativeIncusProjectName(incusProject),
	)
	token, err := p.Trust.CreateToken(ctx, tokenPlan)
	if err != nil {
		return PersonalTenantResult{}, err
	}
	return PersonalTenantResult{
		UserKey:             userKey,
		Tenant:              userKey,
		IncusProject:        incusProject,
		AccessibleTenants:   []string{userKey},
		Token:               token.Token,
		RemoteName:          token.RemoteName,
		Projects:            append([]string{}, token.Projects...),
		CurrentProject:      naming.DefaultProjectName,
		DefaultProjectReady: hasProject(projects, naming.DefaultProjectName),
		TenantTailnetReady:  true,
		Message:             "Personal tenant " + userKey + " is ready.",
	}, nil
}

func (p Provisioner) ensureDefaultProject(ctx context.Context, summary tenant.Summary) ([]meta.Project, error) {
	if hasProject(summary.Projects, naming.DefaultProjectName) {
		return append([]meta.Project{}, summary.Projects...), nil
	}
	if p.ProjectUpdater == nil {
		return nil, fmt.Errorf("Personal Tenant %s is missing Default Project and project updater is not configured", summary.Tenant)
	}
	projects := append([]meta.Project{}, summary.Projects...)
	projects = append(projects, meta.Project{Name: naming.DefaultProjectName})
	if err := p.ProjectUpdater.SetTenantProjects(ctx, summary.IncusName, projects); err != nil {
		return nil, err
	}
	return projects, nil
}

func hasProject(projects []meta.Project, name string) bool {
	for _, project := range projects {
		if project.Name == name {
			return true
		}
	}
	return false
}

func tenantMetadataFromCreatePlan(plan tenant.CreatePlan) (meta.Tenant, error) {
	return meta.ParseTenantConfig(plan.TenantMetadataConfig)
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
