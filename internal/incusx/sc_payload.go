package incusx

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	incus "github.com/lxc/incus/v6/client"

	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

// ensureV2PlatformPayload converges one app project's sc-platform volume onto
// the platform payload built into this binary (ADR-0022). This is the central
// update path: one write per project, never per machine — every machine mounts
// the shared volume, so the guarded shims pick the new payload up on next use.
// Idempotent: a matching VERSION marker is a no-op. Returns whether anything
// was written.
func ensureV2PlatformPayload(server TenantResourceServer, pool string) (bool, error) {
	files, version := tenant.PlatformPayload()
	if current := readSCPlatformVersion(server, pool); current == version {
		return false, nil
	}
	// Parent directories first (the volume file API does not create them).
	dirs := map[string]bool{}
	for _, f := range files {
		for d := path.Dir(f.Path); d != "." && d != "/"; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	ordered := make([]string, 0, len(dirs))
	for d := range dirs {
		ordered = append(ordered, d)
	}
	sort.Strings(ordered) // parents sort before children
	for _, d := range ordered {
		if err := server.CreateStorageVolumeFile(pool, "custom", tenant.V2SCPlatformVolumeName, "/"+d, incus.InstanceFileArgs{
			Type: "directory",
			Mode: 0o755,
		}); err != nil && !strings.Contains(err.Error(), "already exists") {
			return false, fmt.Errorf("create /.sc/platform directory %s: %w", d, err)
		}
	}
	// The builder returns VERSION last, so a partially applied write never
	// advertises the new version — the next sync retries.
	for _, f := range files {
		if err := server.CreateStorageVolumeFile(pool, "custom", tenant.V2SCPlatformVolumeName, "/"+f.Path, incus.InstanceFileArgs{
			Content:   strings.NewReader(f.Content),
			Type:      "file",
			Mode:      f.Mode,
			WriteMode: "overwrite",
		}); err != nil {
			return false, fmt.Errorf("write /.sc/platform/%s: %w", f.Path, err)
		}
	}
	return true, nil
}

// readSCPlatformVersion reports the payload version currently on a project's
// sc-platform volume ("" when none is written yet or the volume is missing).
func readSCPlatformVersion(server TenantResourceServer, pool string) string {
	content, _, err := server.GetStorageVolumeFile(pool, "custom", tenant.V2SCPlatformVolumeName, "/"+tenant.PlatformPayloadVersionFile)
	if err != nil {
		return ""
	}
	defer content.Close()
	data, err := io.ReadAll(content)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
