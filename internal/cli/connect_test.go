package cli

import (
	"fmt"
	"os/exec"
	"testing"
)

func TestShouldRetryCachedConnectFailureOnlyForSSHTransportExit(t *testing.T) {
	if !shouldRetryCachedConnectFailure(wrappedExitError(t, 255)) {
		t.Fatal("exit 255 should retry cached connect")
	}
	for _, code := range []int{0, 1, 2, 130} {
		if shouldRetryCachedConnectFailure(wrappedExitError(t, code)) {
			t.Fatalf("exit %d should not retry cached connect", code)
		}
	}
	if shouldRetryCachedConnectFailure(fmt.Errorf("ssh to machine: command failed")) {
		t.Fatal("non-exit errors should not retry cached connect")
	}
}

func wrappedExitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	if code == 0 {
		return nil
	}
	if err == nil {
		t.Fatalf("exit %d returned nil error", code)
	}
	return fmt.Errorf("ssh to machine default-test: %w", err)
}
