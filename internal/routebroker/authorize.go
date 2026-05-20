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
	principal.Projects = normalizeProjects(principal.Projects)
	return principal, nil
}

func AuthorizeAdd(principal Principal, plan route.AddPlan) error {
	if strings.TrimSpace(principal.Owner) == "" {
		return fmt.Errorf("route principal owner is required")
	}
	if principal.Owner != plan.Project.Owner {
		return fmt.Errorf("owner %s cannot route sandbox owned by %s", principal.Owner, plan.Project.Owner)
	}
	if !principalCanAccessProject(principal, plan.Project.IncusName) {
		return fmt.Errorf("owner %s is not granted access to project %s", principal.Owner, plan.Project.IncusName)
	}
	return nil
}

func AuthorizeRemove(principal Principal, routeMetadata meta.Route, projectPrefix string) error {
	if strings.TrimSpace(principal.Owner) == "" {
		return fmt.Errorf("route principal owner is required")
	}
	if principal.Owner != routeMetadata.TargetOwner {
		return fmt.Errorf("owner %s cannot remove route owned by %s", principal.Owner, routeMetadata.TargetOwner)
	}
	incusProject, err := naming.IncusProjectNameWithPrefix(projectPrefix, naming.ProjectRef{
		Owner:   routeMetadata.TargetOwner,
		Project: routeMetadata.TargetProject,
	})
	if err != nil {
		return err
	}
	if !principalCanAccessProject(principal, incusProject) {
		return fmt.Errorf("owner %s is not granted access to project %s", principal.Owner, incusProject)
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

func normalizeProjects(projects []string) []string {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(projects))
	for _, project := range projects {
		project = strings.TrimSpace(project)
		if project == "" || seen[project] {
			continue
		}
		seen[project] = true
		normalized = append(normalized, project)
	}
	return normalized
}
