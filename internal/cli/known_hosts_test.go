package cli

import (
	"strings"
	"testing"
)

func TestRewriteKnownHostKeysUsesRequestedHost(t *testing.T) {
	input := []byte("# comment\n10.248.0.21 ssh-ed25519 AAAA\n10.248.0.21 ecdsa-sha2-nistp256 BBBB\n")
	filtered := filterKnownHostKeys(input)
	rewritten := string(rewriteKnownHostKeys(filtered, "codex.default.acme"))
	if strings.Contains(rewritten, "10.248.0.21") {
		t.Fatalf("rewritten keys kept scanned host: %q", rewritten)
	}
	for _, want := range []string{
		"codex.default.acme ssh-ed25519 AAAA",
		"codex.default.acme ecdsa-sha2-nistp256 BBBB",
	} {
		if !strings.Contains(rewritten, want) {
			t.Fatalf("rewritten keys %q missing %q", rewritten, want)
		}
	}
}
