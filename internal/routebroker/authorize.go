package routebroker

import (
	"context"
	"fmt"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

type Principal struct {
	Fingerprint string   `json:"fingerprint"`
	User        string   `json:"user"`
	Projects    []string `json:"projects,omitempty"`
}

type TrustMapper interface {
	PrincipalForFingerprint(context.Context, string) (Principal, error)
}

func PrincipalFromFingerprint(ctx context.Context, mapper TrustMapper, fingerprint string) (Principal, error) {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return Principal{}, fmt.Errorf("client certificate fingerprint is required")
	}
	if mapper == nil {
		return Principal{}, fmt.Errorf("trust mapper is required")
	}
	principal, err := mapper.PrincipalForFingerprint(ctx, fingerprint)
	if err != nil {
		return Principal{}, err
	}
	principal.Fingerprint = strings.TrimSpace(principal.Fingerprint)
	if principal.Fingerprint == "" {
		principal.Fingerprint = fingerprint
	}
	principal.User = strings.TrimSpace(principal.User)
	if principal.User == "" {
		return Principal{}, fmt.Errorf("client certificate %s is not mapped to a Sandcastle user", fingerprint)
	}
	if err := naming.ValidateTenantName(principal.User); err != nil {
		return Principal{}, fmt.Errorf("client certificate %s maps to invalid Sandcastle user %q", fingerprint, principal.User)
	}
	projects, err := normalizeProjects(principal.Projects)
	if err != nil {
		return Principal{}, err
	}
	principal.Projects = projects
	return principal, nil
}

func AuthorizeCreate(principal Principal, plan route.CreatePlan) error {
	if strings.TrimSpace(principal.User) == "" {
		return fmt.Errorf("route principal user is required")
	}
	if !principalCanAccessProject(principal, plan.Tenant.IncusName) {
		return fmt.Errorf("user %s is not granted access to tenant %s", principal.User, plan.Tenant.Tenant)
	}
	return nil
}

func AuthorizeDelete(principal Principal, routeMetadata meta.Route, incusProjectPrefix string) error {
	if strings.TrimSpace(principal.User) == "" {
		return fmt.Errorf("route principal user is required")
	}
	incusProject, err := naming.TenantIncusProjectNameWithPrefix(incusProjectPrefix, naming.TenantRef{Tenant: routeMetadata.TargetTenant})
	if err != nil {
		return err
	}
	if !principalCanAccessProject(principal, incusProject) {
		return fmt.Errorf("user %s is not granted access to tenant %s", principal.User, routeMetadata.TargetTenant)
	}
	return nil
}

func principalCanAccessProject(principal Principal, incusProject string) bool {
	for _, project := range principal.Projects {
		if project == incusProject {
			return true
		}
	}
	return false
}

func normalizeProjects(projects []string) ([]string, error) {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(projects))
	for _, project := range projects {
		project = strings.TrimSpace(project)
		if project == "" || seen[project] {
			continue
		}
		if err := naming.ValidateIncusProjectName(project); err != nil {
			return nil, fmt.Errorf("invalid restricted project grant %q: %w", project, err)
		}
		seen[project] = true
		normalized = append(normalized, project)
	}
	return normalized, nil
}
