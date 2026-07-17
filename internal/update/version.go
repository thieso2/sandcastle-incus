package update

import (
	"strings"

	"github.com/blang/semver/v4"
)

// IsDevBuild reports whether the running binary carries no release version:
// the un-stamped "0.0.0-dev" sentinel, a goreleaser snapshot (prerelease
// suffix), or anything that is not plain semver. Dev builds skip the update
// check entirely and are exempt from skew warnings.
func IsDevBuild(version string) bool {
	v, err := semver.Parse(strings.TrimPrefix(version, "v"))
	if err != nil {
		return true
	}
	return len(v.Pre) > 0 || len(v.Build) > 0
}

// IsNewer reports whether the release tag (e.g. "v0.2.0") is strictly newer
// than the running version. Dev builds and unparsable tags are never
// "outdated" — the caller stays silent rather than nagging on guesses.
func IsNewer(latestTag, current string) bool {
	if IsDevBuild(current) {
		return false
	}
	latest, err := semver.Parse(strings.TrimPrefix(latestTag, "v"))
	if err != nil {
		return false
	}
	cur, err := semver.Parse(strings.TrimPrefix(current, "v"))
	if err != nil {
		return false
	}
	return latest.GT(cur)
}
