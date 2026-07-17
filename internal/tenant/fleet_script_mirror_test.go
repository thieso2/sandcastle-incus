package tenant

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scripts/fix-agent-forwarding.sh hand-mirrors the Go shims (it must run over
// bare `incus exec` with no binary on the machine). This pins the mirror to
// the constants so the two can't drift silently: every load-bearing shim line
// the Go side bakes must appear verbatim in the fleet script.
func TestFleetScriptMirrorsShims(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "fix-agent-forwarding.sh"))
	if err != nil {
		t.Fatalf("read fleet script: %v", err)
	}
	body := string(script)
	for _, shim := range []string{SCSSHRCShim, SCShellRCShim} {
		for _, line := range strings.Split(strings.TrimRight(shim, "\n"), "\n") {
			if line == "true" || line == "#!/bin/sh" {
				continue
			}
			if !strings.Contains(body, line) {
				t.Fatalf("fleet script drifted from the Go shims: missing line %q", line)
			}
		}
	}
	if !strings.Contains(body, SCShimMarker) {
		t.Fatalf("fleet script lost the shim marker %q", SCShimMarker)
	}
}
