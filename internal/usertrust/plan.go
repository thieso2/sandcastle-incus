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
		ref, err := naming.ParseProjectRef(raw)
		if err != nil {
			return UserPlan{}, err
		}
		if ref.Owner != base.User {
			return UserPlan{}, fmt.Errorf("user %s cannot be granted project owned by %s", base.User, ref.Owner)
		}
		name, err := naming.IncusProjectNameWithPrefix(admin.ProjectPrefix, ref)
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
		return UserPlan{}, fmt.Errorf("at least one project is required")
	}
	base.Projects = projects
	return base, nil
}

func PlanToken(user string) (UserPlan, error) {
	return PlanCreateUser(user)
}

func RestrictedName(user string) string {
	return CertificateNamePrefix + user
}

func validateUser(user string) error {
	_, err := naming.ParseProjectRef(user + "/placeholder")
	if err != nil {
		return fmt.Errorf("invalid user %q", user)
	}
	return nil
}
