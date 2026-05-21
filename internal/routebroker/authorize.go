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
	Owner       string   `json:"owner"`
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
	principal.Owner = strings.TrimSpace(principal.Owner)
	if principal.Owner == "" {
		return Principal{}, fmt.Errorf("client certificate %s is not mapped to a Sandcastle owner", fingerprint)
	}
	if err := naming.ValidateTenantName(principal.Owner); err != nil {
		return Principal{}, fmt.Errorf("client certificate %s maps to invalid Sandcastle owner %q", fingerprint, principal.Owner)
	}
	projects, err := normalizeProjects(principal.Projects)
	if err != nil {
		return Principal{}, err
	}
	principal.Projects = projects
	return principal, nil
}

func AuthorizeAdd(principal Principal, plan route.AddPlan) error {
	if strings.TrimSpace(principal.Owner) == "" {
		return fmt.Errorf("route principal owner is required")
	}
	if !principalCanAccessProject(principal, plan.Tenant.IncusName) {
		return fmt.Errorf("owner %s is not granted access to tenant %s", principal.Owner, plan.Tenant.Tenant)
	}
	return nil
}

func AuthorizeRemove(principal Principal, routeMetadata meta.Route, projectPrefix string) error {
	if strings.TrimSpace(principal.Owner) == "" {
		return fmt.Errorf("route principal owner is required")
	}
	incusProject, err := naming.TenantIncusProjectNameWithPrefix(projectPrefix, naming.TenantRef{Tenant: routeMetadata.TargetTenant})
	if err != nil {
		return err
	}
	if !principalCanAccessProject(principal, incusProject) {
		return fmt.Errorf("owner %s is not granted access to tenant %s", principal.Owner, routeMetadata.TargetTenant)
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
