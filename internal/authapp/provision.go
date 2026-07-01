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

type AuxProjectEnsurer interface {
	EnsureAuxProjects(ctx context.Context, mainProjectName string, reference string, privateCIDR string, admin config.Admin) error
}

type TenantUnixUserUpdater interface {
	SetTenantUnixUser(ctx context.Context, incusProjectName string, unixUser string) error
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
	UnixUserUpdater TenantUnixUserUpdater
	AuxProjects     AuxProjectEnsurer
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
	// Avoid CIDR collisions with existing tenants (the v2 pool is shared).
	var occupied []string
	if p.Tenants != nil {
		if summaries, err := tenant.List(ctx, p.Tenants); err == nil {
			occupied = tenant.OccupiedCIDRs(summaries)
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
	if p.V2Create != nil {
		return p.ensurePersonalTenantV2(ctx, userKey, user.SSHPublicKey)
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
	unixUser, err := p.unixUserForUser(user)
	if err != nil {
		return PersonalTenantResult{}, err
	}
	if incusProject == "" {
		plan, err := tenant.PlanCreate(p.Admin, tenant.CreateRequest{
			Reference:     userKey,
			OccupiedCIDRs: tenant.OccupiedCIDRs(summaries),
			Personal:      true,
			CreatedBy:     userKey,
			UnixUser:      unixUser,
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
		// Recreate infra/native projects and sidecar instances if missing (e.g. partial delete or failed creation).
		if p.AuxProjects != nil {
			if err := p.AuxProjects.EnsureAuxProjects(ctx, incusProject, existing.Tenant, existing.PrivateCIDR, p.Admin); err != nil {
				return PersonalTenantResult{}, err
			}
		}
		updatedProjects, err := p.ensureDefaultProject(ctx, existing)
		if err != nil {
			return PersonalTenantResult{}, err
		}
		projects = updatedProjects
		if err := p.ensureExistingUnixUser(ctx, existing, unixUser); err != nil {
			return PersonalTenantResult{}, err
		}
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

func (p Provisioner) unixUserForUser(user User) (string, error) {
	if value := strings.TrimSpace(user.LocalUnixUser); value != "" {
		if err := naming.ValidateUnixUsername(value); err != nil {
			return "", err
		}
		return value, nil
	}
	value := strings.TrimSpace(p.DefaultUnixUser)
	if value != "" {
		if err := naming.ValidateUnixUsername(value); err != nil {
			return "", err
		}
	}
	return value, nil
}

func (p Provisioner) ensureExistingUnixUser(ctx context.Context, summary tenant.Summary, unixUser string) error {
	unixUser = strings.TrimSpace(unixUser)
	if unixUser == "" {
		return nil
	}
	if summary.UnixUser != "" && summary.UnixUser != summary.Tenant {
		return nil
	}
	if summary.UnixUser == unixUser {
		return nil
	}
	if p.UnixUserUpdater == nil {
		return fmt.Errorf("Personal Tenant %s needs Unix user %s and tenant Unix user updater is not configured", summary.Tenant, unixUser)
	}
	return p.UnixUserUpdater.SetTenantUnixUser(ctx, summary.IncusName, unixUser)
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
