package usertrust

import (
	"context"
	"fmt"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/naming"
)

const CertificateNamePrefix = "sandcastle-"

type UserPlan struct {
	User            string   `json:"user"`
	CertificateName string   `json:"certificateName"`
	RemoteName      string   `json:"remoteName"`
	Restricted      bool     `json:"restricted"`
	Projects        []string `json:"projects"`
	Description     string   `json:"description"`
}

type GrantRequest struct {
	User     string
	Projects []string
}

type TenantAccessRequest struct {
	Tenant string
	User   string
}

type TenantUsersPlan struct {
	Tenant       string `json:"tenant"`
	IncusProject string `json:"incusProject"`
}

type TenantUsersResult struct {
	Tenant       string   `json:"tenant"`
	IncusProject string   `json:"incusProject"`
	Users        []string `json:"users"`
}

type TokenResult struct {
	User            string   `json:"user"`
	CertificateName string   `json:"certificateName"`
	RemoteName      string   `json:"remoteName"`
	Restricted      bool     `json:"restricted"`
	Projects        []string `json:"projects"`
	Token           string   `json:"token"`
}

type Manager interface {
	Grant(context.Context, UserPlan) error
	Revoke(context.Context, UserPlan) error
	Delete(context.Context, UserPlan) error
	ListTenantUsers(context.Context, TenantUsersPlan) (TenantUsersResult, error)
	CreateToken(context.Context, UserPlan) (TokenResult, error)
}

func PlanCreateUser(user string) (UserPlan, error) {
	if err := validateUser(user); err != nil {
		return UserPlan{}, err
	}
	return UserPlan{
		User:            user,
		CertificateName: RestrictedName(user),
		RemoteName:      RestrictedName(user),
		Restricted:      true,
		Projects:        []string{},
		Description:     "Sandcastle restricted user " + user,
	}, nil
}

func PlanGrant(admin config.Admin, request GrantRequest) (UserPlan, error) {
	base, err := PlanCreateUser(request.User)
	if err != nil {
		return UserPlan{}, err
	}
	if err := admin.Validate(); err != nil {
		return UserPlan{}, err
	}
	seenProjects := map[string]bool{}
	projects := make([]string, 0, len(request.Projects))
	for _, raw := range request.Projects {
		ref, err := naming.ParseTenantRef(raw)
		if err != nil {
			return UserPlan{}, err
		}
		name, err := naming.TenantIncusProjectNameWithPrefix(admin.ProjectPrefix, ref)
		if err != nil {
			return UserPlan{}, err
		}
		if seenProjects[name] {
			continue
		}
		seenProjects[name] = true
		projects = append(projects, name)
	}
	if len(projects) == 0 {
		return UserPlan{}, fmt.Errorf("at least one tenant is required")
	}
	base.Projects = projects
	return base, nil
}

func PlanTenantGrant(admin config.Admin, request TenantAccessRequest) (UserPlan, error) {
	return planTenantAccess(admin, request)
}

func PlanTenantRevoke(admin config.Admin, request TenantAccessRequest) (UserPlan, error) {
	return planTenantAccess(admin, request)
}

func PlanTenantUsers(admin config.Admin, tenant string) (TenantUsersPlan, error) {
	if err := admin.Validate(); err != nil {
		return TenantUsersPlan{}, err
	}
	ref, err := naming.ParseTenantRef(tenant)
	if err != nil {
		return TenantUsersPlan{}, err
	}
	incusProject, err := naming.TenantIncusProjectNameWithPrefix(admin.ProjectPrefix, ref)
	if err != nil {
		return TenantUsersPlan{}, err
	}
	return TenantUsersPlan{Tenant: ref.Tenant, IncusProject: incusProject}, nil
}

func PlanToken(user string) (UserPlan, error) {
	return PlanCreateUser(user)
}

func PlanDeleteUser(user string) (UserPlan, error) {
	return PlanCreateUser(user)
}

func RestrictedName(user string) string {
	return CertificateNamePrefix + user
}

func validateUser(user string) error {
	if err := naming.ValidateTenantName(user); err != nil {
		return fmt.Errorf("invalid user %q", user)
	}
	return nil
}

func planTenantAccess(admin config.Admin, request TenantAccessRequest) (UserPlan, error) {
	if err := validateUser(request.User); err != nil {
		return UserPlan{}, err
	}
	return PlanGrant(admin, GrantRequest{
		User:     request.User,
		Projects: []string{request.Tenant},
	})
}
