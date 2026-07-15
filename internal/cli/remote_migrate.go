package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/usertrust"
	"gopkg.in/yaml.v2"
)

// localRemote is one incus remote as read from the client config: its name, the
// endpoint it points at, and the incus project it is pinned to.
type localRemote struct {
	Name     string
	Endpoint string
	Project  string
}

// remoteRename is one legacy remote to rename to the ADR-0020 scheme.
type remoteRename struct {
	From      string
	To        string
	IsCurrent bool
}

// planRemoteMigration computes the renames that bring a tenant's legacy incus
// remotes onto the ADR-0020 `<suffix>-<project>` scheme (issue #88). A remote is
// this tenant's iff its pinned incus project is `sc2-<tenant>-<proj>`; the new
// name is `<suffix>-<proj>`. Renames are scoped by installEndpoint so that a
// same-named tenant on a DIFFERENT install (its remotes point elsewhere) is left
// alone. Already-migrated remotes (new name == current name) and infra-pinned
// remotes (no short project) are skipped, making the plan idempotent.
func planRemoteMigration(remotes []localRemote, tenant, suffix, currentRemote, installEndpoint string) []remoteRename {
	suffix = strings.TrimSpace(suffix)
	tenant = strings.TrimSpace(tenant)
	if suffix == "" || tenant == "" {
		return nil
	}
	var plan []remoteRename
	for _, r := range remotes {
		if installEndpoint != "" && r.Endpoint != installEndpoint {
			continue // a different install's remote — never rename across installs
		}
		proj := shortProjectName(r.Project, tenant)
		if proj == "" {
			continue // not a per-project remote of this tenant (or the infra project)
		}
		newName := usertrust.RemoteNameForSuffixProject(suffix, proj)
		if newName == "" || newName == r.Name {
			continue // idempotent: already on the new scheme
		}
		plan = append(plan, remoteRename{From: r.Name, To: newName, IsCurrent: r.Name == currentRemote})
	}
	return plan
}

// readLocalRemotes reads the incus config.yml in incusDir into localRemotes.
func readLocalRemotes(incusDir string) ([]localRemote, error) {
	data, err := os.ReadFile(filepath.Join(incusDir, "config.yml"))
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Remotes map[string]struct {
			Addr    string `yaml:"addr"`
			Project string `yaml:"project"`
		} `yaml:"remotes"`
	}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	remotes := make([]localRemote, 0, len(parsed.Remotes))
	for name, r := range parsed.Remotes {
		remotes = append(remotes, localRemote{Name: name, Endpoint: r.Addr, Project: r.Project})
	}
	return remotes, nil
}

// migrateLegacyRemotes lazily renames a tenant's legacy remotes to the ADR-0020
// scheme at login (#88). Best-effort: rename failures (e.g. a target name already
// taken locally — the cross-install collision guard) are logged and skipped, and
// the caller's login is never failed by a migration hiccup. Returns the possibly
// updated current-remote name so the caller can re-point config.
func migrateLegacyRemotes(ctx context.Context, incusDir, tenant, suffix, currentRemote, installEndpoint string, stderr io.Writer) string {
	remotes, err := readLocalRemotes(incusDir)
	if err != nil {
		return currentRemote
	}
	plan := planRemoteMigration(remotes, tenant, suffix, currentRemote, installEndpoint)
	updatedCurrent := currentRemote
	for _, rename := range plan {
		args := []string{"remote", "rename", rename.From, rename.To}
		cmd := exec.CommandContext(ctx, "incus", args...)
		cmd.Env = os.Environ()
		if strings.TrimSpace(incusDir) != "" {
			cmd.Env = append(cmd.Env, "INCUS_CONF="+incusDir)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			if stderr != nil {
				fmt.Fprintf(stderr, "Note: could not migrate remote %q → %q: %s\n", rename.From, rename.To, strings.TrimSpace(string(out)))
			}
			continue
		}
		if rename.IsCurrent {
			updatedCurrent = rename.To
		}
	}
	return updatedCurrent
}
