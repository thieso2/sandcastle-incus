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
	From string
	To   string
}

// planRemoteMigration computes the renames that bring a tenant's legacy incus
// remotes onto the ADR-0020 `<suffix>-<project>` scheme (issue #88). A remote is
// this tenant's iff its pinned incus project is `sc2-<tenant>-<proj>`; the new
// name is `<suffix>-<proj>`. Already-migrated remotes (new name == current name)
// and infra-pinned remotes (no short project) are skipped, making the plan
// idempotent.
//
// The installEndpoint scope is MANDATORY, not best-effort: a same-named tenant on
// a different install has the same pinned-project pattern, so without knowing this
// install's endpoint we cannot tell them apart — and renaming the wrong install's
// remotes is worse than not migrating. An empty endpoint therefore yields no plan.
func planRemoteMigration(remotes []localRemote, tenant, suffix, installEndpoint string) []remoteRename {
	suffix = strings.TrimSpace(suffix)
	tenant = strings.TrimSpace(tenant)
	installEndpoint = strings.TrimSpace(installEndpoint)
	if suffix == "" || tenant == "" || installEndpoint == "" {
		return nil // fail safe: never migrate unscoped
	}
	var plan []remoteRename
	for _, r := range remotes {
		if strings.TrimSpace(r.Endpoint) != installEndpoint {
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
		plan = append(plan, remoteRename{From: r.Name, To: newName})
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
// the caller's login is never failed by a migration hiccup. installEndpoint must
// be this install's endpoint; an empty one migrates nothing (see planRemoteMigration).
func migrateLegacyRemotes(ctx context.Context, incusDir, tenant, suffix, installEndpoint string, stderr io.Writer) {
	remotes, err := readLocalRemotes(incusDir)
	if err != nil {
		return
	}
	byName := make(map[string]localRemote, len(remotes))
	for _, r := range remotes {
		byName[r.Name] = r
	}
	for _, rename := range planRemoteMigration(remotes, tenant, suffix, installEndpoint) {
		// The canonical target may already exist — `sc login` enrolls
		// `<suffix>-default` before this hook runs, so a legacy base remote for
		// the same (endpoint, project) is now a redundant DUPLICATE. Remove it
		// rather than leave two remotes for one place. A target that exists but
		// points elsewhere is a genuine cross-install name clash: leave both and
		// surface it (never clobber).
		if target, exists := byName[rename.To]; exists {
			from := byName[rename.From]
			if target.Endpoint == from.Endpoint && target.Project == from.Project {
				runIncusRemote(ctx, incusDir, stderr, rename.From, rename.To, "remove", rename.From)
			} else if stderr != nil {
				fmt.Fprintf(stderr, "Note: remote %q left as-is — %q already exists for a different install\n", rename.From, rename.To)
			}
			continue
		}
		runIncusRemote(ctx, incusDir, stderr, rename.From, rename.To, "rename", rename.From, rename.To)
	}
}

// runIncusRemote runs `incus remote <args...>` in incusDir, logging a best-effort
// migration note (naming from→to) on failure without failing the caller.
func runIncusRemote(ctx context.Context, incusDir string, stderr io.Writer, from, to string, args ...string) {
	cmd := exec.CommandContext(ctx, "incus", append([]string{"remote"}, args...)...)
	cmd.Env = os.Environ()
	if strings.TrimSpace(incusDir) != "" {
		cmd.Env = append(cmd.Env, "INCUS_CONF="+incusDir)
	}
	if out, err := cmd.CombinedOutput(); err != nil && stderr != nil {
		fmt.Fprintf(stderr, "Note: could not migrate remote %q → %q: %s\n", from, to, strings.TrimSpace(string(out)))
	}
}
