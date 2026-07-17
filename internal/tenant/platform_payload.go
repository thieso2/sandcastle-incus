package tenant

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// The per-tenant /.sc shared-scripts volume (spec #127) presents two layers to
// every machine: a platform-managed base and a tenant-owned overlay. Machines
// carry only stable shims at the fixed OS paths; the shims source the payload
// from these mount points, platform first, local second.
const (
	SCPlatformPath = "/.sc/platform"
	SCLocalPath    = "/.sc/local"
)

// PlatformPayloadFile is one file of the /.sc/platform payload. Path is
// relative to SCPlatformPath.
type PlatformPayloadFile struct {
	Path    string `json:"path"`
	Mode    int    `json:"mode"`
	Content string `json:"content"`
}

// PlatformPayloadVersionFile is the payload's version marker: a file inside
// /.sc/platform whose content is the payload version, so any machine (and the
// central updater) can tell which payload a tenant is running.
const PlatformPayloadVersionFile = "VERSION"

// Payload script paths (relative to SCPlatformPath). These are the exact paths
// the baked shims source — the shim↔payload contract test keeps the two sides
// in agreement, so a shim can never point at a script the payload doesn't ship.
const (
	SCPayloadSSHRCPath   = "ssh/sshrc"
	SCPayloadShellRCPath = "shell/rc.sh"
)

// PlatformPayload returns the versioned /.sc/platform payload: every platform
// script as a file set plus the payload version. The content comes from the
// same shared constants the agent-forwarding work established (one source of
// truth), so the payload, the profile-era scripts, and the `sc fix` backfill
// can never drift apart.
//
// The version is derived from the file contents, so it is stable for a given
// payload and changes exactly when the payload does. The VERSION marker file
// is part of the returned set.
func PlatformPayload() ([]PlatformPayloadFile, string) {
	files := []PlatformPayloadFile{
		{Path: SCPayloadSSHRCPath, Mode: 0o755, Content: sshAgentRepublishScript},
		{Path: SCPayloadShellRCPath, Mode: 0o644, Content: sshAgentConsumeSnippet},
	}
	version := platformPayloadVersion(files)
	files = append(files, PlatformPayloadFile{Path: PlatformPayloadVersionFile, Mode: 0o644, Content: version + "\n"})
	return files, version
}

// platformPayloadVersion hashes the payload's (path, mode, content) tuples in
// order. Content-derived: two binaries shipping identical scripts agree on the
// version; any script change (or an older binary re-run for rollback) yields
// its own version, making drift detectable and rollback a plain re-sync.
func platformPayloadVersion(files []PlatformPayloadFile) string {
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%o\x00%s\x00", f.Path, f.Mode, f.Content)
	}
	return "sc-payload-" + hex.EncodeToString(h.Sum(nil))[:16]
}

// PlatformPayloadVersion returns the version of the payload built into this
// binary — what a central sync would write.
func PlatformPayloadVersion() string {
	_, version := PlatformPayload()
	return version
}

