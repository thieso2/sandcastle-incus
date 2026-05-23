package infra

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
)

func TestSeedRoundTripWritesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lab.seed.yml")
	seed := SeedFromAdmin("lab", config.AdminDefaults(), "alice")
	seed = EmbedCaddyDataArchive(seed, "auth.example.com", []byte("archive"))

	if err := SaveSeed(path, seed); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	loaded, ok, err := LoadSeed(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected seed to load")
	}
	if loaded.Deployment != "lab" || loaded.Auth.DefaultUnixUser != "alice" {
		t.Fatalf("loaded seed = %#v", loaded)
	}
	if loaded.TLS.CaddyDataArchiveBase64 != base64.StdEncoding.EncodeToString([]byte("archive")) {
		t.Fatalf("archive = %q", loaded.TLS.CaddyDataArchiveBase64)
	}
}

func TestResolveSeedAdminEnvironmentOverridesSeed(t *testing.T) {
	clearSeedEnvForTest(t)
	t.Setenv("SANDCASTLE_REMOTE", "env-remote")
	t.Setenv("SANDCASTLE_INFRA_PROJECT", "env-infra")

	admin := config.AdminDefaults()
	admin.Remote = "seed-remote"
	admin.InfrastructureProject = "seed-infra"
	seed := SeedFromAdmin("lab", admin, "alice")

	resolved := ResolveSeedAdmin(seed)
	if resolved.Remote != "env-remote" {
		t.Fatalf("Remote = %q", resolved.Remote)
	}
	if resolved.InfrastructureProject != "env-infra" {
		t.Fatalf("InfrastructureProject = %q", resolved.InfrastructureProject)
	}
	if resolved.StoragePool != config.DefaultStoragePool {
		t.Fatalf("StoragePool = %q", resolved.StoragePool)
	}
}

func TestCaddyDataArchiveBytesRejectsHostnameMismatch(t *testing.T) {
	seed := EmbedCaddyDataArchive(SeedFromAdmin("lab", config.AdminDefaults(), "alice"), "auth.example.com", []byte("archive"))
	_, _, err := CaddyDataArchiveBytes(seed, "other.example.com")
	if err == nil {
		t.Fatal("expected hostname mismatch error")
	}
}

func clearSeedEnvForTest(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"SANDCASTLE_REMOTE",
		"SANDCASTLE_INFRA_PROJECT",
		"SANDCASTLE_STORAGE_POOL",
		"SANDCASTLE_CIDR_POOL",
		"SANDCASTLE_INCUS_PROJECT_PREFIX",
		"SANDCASTLE_INFRA_TLS_MODE",
		"SANDCASTLE_AUTH_HOSTNAME",
		"SANDCASTLE_BASE_IMAGE",
		"SANDCASTLE_AI_IMAGE",
	} {
		t.Setenv(key, "")
	}
}
