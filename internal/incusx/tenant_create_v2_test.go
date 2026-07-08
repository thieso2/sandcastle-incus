package incusx

import (
	"strings"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
)

// exitCodeOperation stubs an exec operation whose command exited with a code.
type exitCodeOperation struct {
	fakeOperation
	code float64
}

func (o exitCodeOperation) Get() api.Operation {
	return api.Operation{Metadata: map[string]any{"return": o.code}}
}

// Regression: the SDK's op.Wait() only fails when the OPERATION fails — a
// script that ran and exited nonzero still "succeeds". execSidecar callers
// relied on Wait alone, so every sidecar provisioning failure (apt racing the
// container boot, download errors) was silently swallowed and `tenant create`
// reported success with no CoreDNS/Tailscale installed (caught live on
// majestix). Nonzero command exits must surface as errors.
func TestExecExitErrorSurfacesNonzeroCommandExit(t *testing.T) {
	if err := execExitError(exitCodeOperation{code: 0}, ""); err != nil {
		t.Fatalf("exit 0 must not error: %v", err)
	}
	if err := execExitError(fakeOperation{}, ""); err != nil {
		t.Fatalf("missing metadata must not error: %v", err)
	}
	err := execExitError(exitCodeOperation{code: 2}, "apt-get: temporary failure resolving deb.debian.org")
	if err == nil {
		t.Fatal("exit 2 must error")
	}
	for _, want := range []string{"status 2", "temporary failure"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}
