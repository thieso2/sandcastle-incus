package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPlatformIncusDir checks the per-OS native incus dir resolution used by
// the admin CLI to find admin certs without an explicit INCUS_CONF. It drives
// os.UserConfigDir via HOME (and clears XDG_CONFIG_HOME so Linux uses
// HOME/.config), so it stays correct on both Linux and macOS.
func TestPlatformIncusDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", "")

	// No config.yml at the native location yet ⇒ "" so admin callers fall back
	// to the SDK default unchanged.
	if got := PlatformIncusDir(); got != "" {
		t.Fatalf("PlatformIncusDir before config exists = %q, want empty", got)
	}

	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	dir := filepath.Join(base, "incus")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte("remotes: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// With a real incus config present, it is returned.
	if got := PlatformIncusDir(); got != dir {
		t.Fatalf("PlatformIncusDir = %q, want %q", got, dir)
	}
}
