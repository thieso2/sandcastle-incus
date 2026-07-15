package cli

import (
	"fmt"
	"os"
	"strings"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

// resolveConnectTarget decides whether a parsed machine reference targets a
// DIFFERENT install than the current one and, if so, which incus remote to
// switch to (ADR-0020). dnsSuffix/project come from the reference; currentSuffix
// is the current install's Tenant DNS Suffix; remoteExists reports whether a
// named incus remote is enrolled locally. Returns "" for a same-install
// reference (no switch), or a guidance error (ADR-0020 §7) when the target
// install/project has no local remote.
func resolveConnectTarget(dnsSuffix, project, currentSuffix string, remoteExists func(string) bool, installKnown func(string) bool) (switchTo string, err error) {
	dnsSuffix = strings.TrimSpace(dnsSuffix)
	if dnsSuffix == "" || dnsSuffix == strings.TrimSpace(currentSuffix) {
		return "", nil // same install — nothing to switch
	}
	target := usertrust.RemoteNameForSuffixProject(dnsSuffix, project)
	if target == "" {
		return "", fmt.Errorf("cannot form a remote name for install %q project %q", dnsSuffix, project)
	}
	if remoteExists(target) {
		return target, nil
	}
	// No remote for this (install, project). Connect never auto-provisions
	// (ADR-0020 §7); distinguish the two guidance cases the spec calls out.
	if installKnown(dnsSuffix) {
		return "", fmt.Errorf(
			"no remote %q for install %q — the project isn't enrolled; enroll it first:\n  sc enroll   (or: sc project create %s)",
			target, dnsSuffix, project)
	}
	return "", fmt.Errorf(
		"unknown install %q — you're not logged in there; log in first:\n  sc login <%s auth-host>",
		dnsSuffix, dnsSuffix)
}

// localRemoteExists reports whether an incus remote of that name is enrolled in
// any of the client's config dirs (shared or per-remote).
func localRemoteExists(name string) bool {
	return scconfig.ResolveConfigPath(name) != ""
}

// localInstallKnown reports whether the client has logged into the install with
// this DNS suffix — i.e. its default remote `<suffix>-default` is enrolled. Used
// only to choose between the "enroll the project" and "log in first" guidance.
func localInstallKnown(suffix string) bool {
	return scconfig.ResolveConfigPath(strings.TrimSpace(suffix)+"-default") != ""
}

// switchConfigToRemote returns a copy of cfg rebound to targetRemote: it points
// INCUS_CONF at that remote's config dir (its restricted cert), rebuilds the two
// remote-scoped stores connect uses (the tenant summary source and the
// machine-ensure client), and — crucially — re-points adminConfig.Tenant to the
// TARGET install's tenant. The summary lookup keys off the tenant name, so
// without this it would still resolve the *current* tenant on the new remote.
// project is the reference's project, used to strip the pinned incus project
// (`<prefix>-<tenant>-<project>`) back to its tenant. All other (local)
// capabilities carry over unchanged.
func switchConfigToRemote(cfg commandConfig, targetRemote, project string) commandConfig {
	dir := scconfig.ResolveConfigPath(targetRemote)
	if dir != "" {
		os.Setenv("INCUS_CONF", dir)
	}
	switched := cfg
	switched.adminConfig.Remote = targetRemote
	switched.adminConfig.Project = "" // the reference carries the project
	switched.tenantStore = incusx.NewTenantStoreForSharedRemote(incusx.NewSharedRemote(targetRemote))
	switched.tenantCreator = incusx.NewTenantCreator(targetRemote)
	if remotes, err := readLocalRemotes(dir); err == nil {
		for _, r := range remotes {
			if r.Name == targetRemote {
				if tenant := tenantFromPinnedProject(r.Project, project); tenant != "" {
					switched.adminConfig.Tenant = tenant
				}
				break
			}
		}
	}
	return switched
}

// tenantFromPinnedProject recovers the tenant from a remote's pinned incus
// project `<prefix>-<tenant>-<project>` given the known project. The trailing
// `-<project>` is stripped (handles dashed projects), then the single-token
// prefix (e.g. "sc2") before the first dash — leaving the tenant (which may
// itself contain dashes). Returns "" if the shape doesn't match.
func tenantFromPinnedProject(pinnedProject, project string) string {
	rest := strings.TrimSuffix(strings.TrimSpace(pinnedProject), "-"+strings.TrimSpace(project))
	if rest == pinnedProject || rest == "" {
		return "" // project suffix didn't match
	}
	if i := strings.Index(rest, "-"); i >= 0 {
		return rest[i+1:]
	}
	return ""
}
