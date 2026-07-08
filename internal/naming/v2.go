package naming

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
)

// V2 naming (ADR-0016). In v2 the boundary is the Tenant (admin-minted handle)
// and each Project is its own Incus project sharing one per-tenant bridge:
//
//	sc2-<tenant>            per-tenant infra project (holds the one sidecar)
//	sc2-<tenant>-<project>  one Incus project per project (app machines)
//	sc2-<tenant>            the shared per-tenant bridge (in the default project)
//
// The default prefix is "sc2" so v2 coexists with v1's "sc-*" projects on the
// same Incus host.
const V2IncusProjectPrefix = "sc2"

// V2SidecarInstanceName is the Incus instance name of every v2 tenant sidecar.
// It is deliberately unqualified ("sidecar"): the instance lives inside the
// tenant's own infra project, so the project already carries the identity and
// repeating it in the instance name is redundant. The sidecar's *global* names
// (its tailnet hostname) are set separately where cross-tenant uniqueness is
// actually required.
const V2SidecarInstanceName = "sidecar"

// maxIncusProjectNameLen is Incus's practical instance/project name budget.
const maxIncusProjectNameLen = 63

// V2TenantInfraProjectName returns the per-tenant infra Incus project name
// (sc2-<tenant>) that holds the tenant's single sidecar.
func V2TenantInfraProjectName(prefix string, tenant string) (string, error) {
	prefix = strings.TrimSpace(prefix)
	if err := ValidateIncusProjectPrefix(prefix); err != nil {
		return "", err
	}
	if err := ValidateTenantName(tenant); err != nil {
		return "", err
	}
	name := prefix + "-" + tenant
	if len(name) > maxIncusProjectNameLen {
		return "", fmt.Errorf("incus project name %q exceeds %d characters", name, maxIncusProjectNameLen)
	}
	return name, nil
}

// V2ProjectName returns the per-project Incus project name (sc2-<tenant>-<project>).
func V2ProjectName(prefix string, tenant string, project string) (string, error) {
	infra, err := V2TenantInfraProjectName(prefix, tenant)
	if err != nil {
		return "", err
	}
	if err := ValidateProjectName(project); err != nil {
		return "", err
	}
	name := infra + "-" + project
	if len(name) > maxIncusProjectNameLen {
		return "", fmt.Errorf("incus project name %q exceeds %d characters", name, maxIncusProjectNameLen)
	}
	return name, nil
}

// V2BridgeName returns the shared per-tenant bridge name. Bridge (Linux
// interface) names are limited to 15 chars, so long tenant handles fall back to
// a stable hashed name instead of a truncation that could collide.
func V2BridgeName(prefix string, tenant string) (string, error) {
	infra, err := V2TenantInfraProjectName(prefix, tenant)
	if err != nil {
		return "", err
	}
	if len(infra) <= 15 {
		return infra, nil
	}
	sum := sha1.Sum([]byte(infra))
	return "sc2-" + hex.EncodeToString(sum[:])[:11], nil
}
