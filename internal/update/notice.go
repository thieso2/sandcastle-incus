package update

import (
	"os"
	"time"
)

// checkInterval is how often the release check runs and how often the
// passive notice may repeat (per target).
const checkInterval = 24 * time.Hour

// releaseGrace suppresses announcements for releases younger than 24h so the
// Homebrew tap has time to catch up before users are told to upgrade.
const releaseGrace = 24 * time.Hour

// NoUpdateNotifierEnv disables the release check and passive notice entirely.
const NoUpdateNotifierEnv = "SANDCASTLE_NO_UPDATE_NOTIFIER"

// CheckDue reports whether a fresh release check should run now: at most
// once per 24h, never for dev builds, never when the opt-out env is set.
func CheckDue(st State, current string, now time.Time) bool {
	if IsDevBuild(current) || os.Getenv(NoUpdateNotifierEnv) != "" {
		return false
	}
	return now.Sub(st.CheckedAt) >= checkInterval
}

// NoticeDue reports whether the passive "new release available" notice
// should print now, based on the cached check result: only when the cached
// latest is strictly newer, the release is past the 24h tap-catch-up grace,
// and no notice was printed in the last 24h.
func NoticeDue(st State, current string, now time.Time) bool {
	if os.Getenv(NoUpdateNotifierEnv) != "" {
		return false
	}
	if !IsNewer(st.LatestTag, current) {
		return false
	}
	if !st.PublishedAt.IsZero() && now.Sub(st.PublishedAt) < releaseGrace {
		return false
	}
	return now.Sub(st.NoticedAt) >= checkInterval
}

// SidecarNoticeDue reports whether the "your sidecar is behind the
// deployment" notice should print: both versions were observed this run
// (they ride the version exchange — no extra call), the sidecar is strictly
// older, and this target's notice was not printed in the last 24h.
func SidecarNoticeDue(st State, sidecarVersion, deploymentVersion string, now time.Time) bool {
	if os.Getenv(NoUpdateNotifierEnv) != "" {
		return false
	}
	if sidecarVersion == "" || deploymentVersion == "" {
		return false
	}
	if !IsNewer(deploymentVersion, sidecarVersion) {
		return false
	}
	return now.Sub(st.SidecarNoticedAt) >= checkInterval
}
