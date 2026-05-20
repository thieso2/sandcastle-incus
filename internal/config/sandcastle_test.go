package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSandcastleConfigMissingFileReturnsEmpty(t *testing.T) {
	cfg, err := LoadSandcastleConfig(filepath.Join(t.TempDir(), "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Owner != "" || cfg.Remote != "" {
		t.Fatalf("expected empty config, got %+v", cfg)
	}
}

func TestLoadSandcastleConfigReadsOwnerAndRemote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("owner: alice\nremote: sandcastle-alice\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadSandcastleConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Owner != "alice" || cfg.Remote != "sandcastle-alice" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestSaveAndReloadSandcastleConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := SaveSandcastleConfig(path, SandcastleConfig{Owner: "bob", Remote: "prod"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadSandcastleConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Owner != "bob" || cfg.Remote != "prod" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadAdminFromFileAndEnvEnvWins(t *testing.T) {
	t.Setenv("SANDCASTLE_OWNER", "env-owner")
	t.Setenv("SANDCASTLE_REMOTE", "env-remote")
	admin := loadAdminFromFileAndEnv(SandcastleConfig{Owner: "file-owner", Remote: "file-remote"})
	if admin.Owner != "env-owner" {
		t.Fatalf("Owner = %q, want env-owner", admin.Owner)
	}
	if admin.Remote != "env-remote" {
		t.Fatalf("Remote = %q, want env-remote", admin.Remote)
	}
}

func TestLoadAdminFromFileAndEnvFileUsedWhenNoEnv(t *testing.T) {
	t.Setenv("SANDCASTLE_OWNER", "")
	t.Setenv("SANDCASTLE_REMOTE", "")
	admin := loadAdminFromFileAndEnv(SandcastleConfig{Owner: "file-owner", Remote: "file-remote"})
	if admin.Owner != "file-owner" {
		t.Fatalf("Owner = %q, want file-owner", admin.Owner)
	}
	if admin.Remote != "file-remote" {
		t.Fatalf("Remote = %q, want file-remote", admin.Remote)
	}
}

func TestLoadAdminFromFileAndEnvDefaultRemoteWhenBothEmpty(t *testing.T) {
	t.Setenv("SANDCASTLE_REMOTE", "")
	admin := loadAdminFromFileAndEnv(SandcastleConfig{})
	if admin.Remote != DefaultRemote {
		t.Fatalf("Remote = %q, want %q", admin.Remote, DefaultRemote)
	}
}

func TestResolveConfigPathReturnsEmptyWhenDirMissing(t *testing.T) {
	if resolveConfigPath("nonexistent-remote") != "" {
		t.Fatal("expected empty config path for missing remote")
	}
}
