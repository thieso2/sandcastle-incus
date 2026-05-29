package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/incusx"
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

func TestLocalKnownHostsManagerUsesRecentKeyscanCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	remote := "sandcastle-test"
	tenant := "acme"
	path := incusx.TenantKnownHostsPath(remote, tenant)
	if err := ensureKnownHostsFile(path); err != nil {
		t.Fatal(err)
	}
	content := "dev.io.acme ssh-ed25519 AAAA\n10.248.1.20 ssh-ed25519 AAAA\n"
	if err := appendKnownHostsFixture(path, content); err != nil {
		t.Fatal(err)
	}
	cache := incusx.NewConnectCache(remote)
	cache.MarkKeyscanned("dev.io.acme")
	cache.MarkKeyscanned("10.248.1.20")
	manager := newLocalKnownHostsManager(remote, false, nil).WithConnectCache(cache)

	if !manager.knownHostsRecentlyRefreshed(path, "dev.io.acme", "10.248.1.20") {
		t.Fatal("expected known_hosts cache hit")
	}
}

func TestLocalKnownHostsManagerRequiresHostEntriesForRecentKeyscanCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	remote := "sandcastle-test"
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := appendKnownHostsFixture(path, "dev.io.acme ssh-ed25519 AAAA\n"); err != nil {
		t.Fatal(err)
	}
	cache := incusx.NewConnectCache(remote)
	cache.MarkKeyscanned("dev.io.acme")
	cache.MarkKeyscanned("10.248.1.20")
	manager := newLocalKnownHostsManager(remote, false, nil).WithConnectCache(cache)

	if manager.knownHostsRecentlyRefreshed(path, "dev.io.acme", "10.248.1.20") {
		t.Fatal("expected cache miss when private IP entry is absent")
	}
}

func appendKnownHostsFixture(path string, content string) error {
	if err := ensureKnownHostsFile(path); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}
