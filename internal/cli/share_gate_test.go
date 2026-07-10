package cli

import (
	"strings"
	"testing"
)

// Tenant Storage Shares are gated off on v2 (#70). Every `sc share` subcommand
// must refuse with the not-supported message rather than half-run against the
// dormant plumbing.
func TestShareCommandsAreGatedOnV2(t *testing.T) {
	invocations := [][]string{
		{"share", "create", "default:/workspace/x", "--to", "acme"},
		{"share", "list"},
		{"share", "status", "acme/default/x"},
		{"share", "offers"},
		{"share", "accept", "acme/default/x"},
		{"share", "decline", "acme/default/x"},
		{"share", "revoke", "default/x", "--tenant", "acme"},
		{"share", "delete", "default/x", "--yes"},
		{"share", "reconcile"},
	}
	for _, args := range invocations {
		_, err := executeForTestWithConfig(t, commandConfig{name: "sandcastle"}, args...)
		if err == nil {
			t.Fatalf("%v: expected the share gate to refuse, got nil", args)
		}
		if !strings.Contains(err.Error(), "not yet supported on v2") {
			t.Fatalf("%v: error = %v", args, err)
		}
	}
}
