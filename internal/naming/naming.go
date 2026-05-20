package naming

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	DefaultProjectPrefix = "sc"
)

var safeNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

type ProjectRef struct {
	Owner   string
	Project string
}

func ParseProjectRef(value string) (ProjectRef, error) {
	owner, project, ok := strings.Cut(value, "/")
	if !ok {
		return ProjectRef{}, fmt.Errorf("project reference must be owner/project")
	}
	ref := ProjectRef{Owner: owner, Project: project}
	if err := ref.Validate(); err != nil {
		return ProjectRef{}, err
	}
	return ref, nil
}

func ParseProjectRefWithDefaultOwner(value string, defaultOwner string) (ProjectRef, error) {
	if strings.Contains(value, "/") {
		return ParseProjectRef(value)
	}
	if strings.TrimSpace(defaultOwner) == "" {
		return ProjectRef{}, fmt.Errorf("project reference must be owner/project or set SANDCASTLE_OWNER to use project")
	}
	return ParseProjectRef(defaultOwner + "/" + value)
}

func (r ProjectRef) Validate() error {
	if !safeNamePattern.MatchString(r.Owner) {
		return fmt.Errorf("invalid owner %q", r.Owner)
	}
	if !safeNamePattern.MatchString(r.Project) {
		return fmt.Errorf("invalid project %q", r.Project)
	}
	return nil
}

func (r ProjectRef) String() string {
	return r.Owner + "/" + r.Project
}

func IncusProjectName(ref ProjectRef) (string, error) {
	return IncusProjectNameWithPrefix(DefaultProjectPrefix, ref)
}

func IncusProjectNameWithPrefix(prefix string, ref ProjectRef) (string, error) {
	if prefix == "" {
		return "", fmt.Errorf("project prefix is required")
	}
	if err := ref.Validate(); err != nil {
		return "", err
	}
	name := prefix + "-" + ref.Owner + "-" + ref.Project
	if len(name) > 63 {
		return "", fmt.Errorf("incus project name %q exceeds 63 characters", name)
	}
	return name, nil
}

func IsReservedSandboxName(name string) bool {
	switch name {
	case "ca", "dns", "tailscale", "sc-ca", "sc-dns", "sc-tailscale":
		return true
	default:
		return false
	}
}
