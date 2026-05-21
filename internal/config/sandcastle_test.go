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
	if cfg.Tenant != "" || cfg.Project != "" || cfg.Remote != "" {
		t.Fatalf("expected empty config, got %+v", cfg)
	}
}

func TestLoadSandcastleConfigReadsTenantProjectAndRemote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("tenant: acme\nproject: website\nremote: sandcastle-acme\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadSandcastleConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tenant != "acme" || cfg.Project != "website" || cfg.Remote != "sandcastle-acme" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestSaveAndReloadSandcastleConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := SaveSandcastleConfig(path, SandcastleConfig{Tenant: "acme", Project: "website", Remote: "prod"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadSandcastleConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tenant != "acme" || cfg.Project != "website" || cfg.Remote != "prod" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadAdminFromFileAndEnvEnvWins(t *testing.T) {
	t.Setenv("SANDCASTLE_TENANT", "env-tenant")
	t.Setenv("SANDCASTLE_REMOTE", "env-remote")
	admin := loadAdminFromFileAndEnv(SandcastleConfig{Tenant: "file-tenant", Remote: "file-remote"})
	if admin.Tenant != "env-tenant" {
		t.Fatalf("Tenant = %q, want env-tenant", admin.Tenant)
	}
	if admin.Remote != "env-remote" {
		t.Fatalf("Remote = %q, want env-remote", admin.Remote)
	}
}

func TestLoadAdminFromFileAndEnvFileUsedWhenNoEnv(t *testing.T) {
	t.Setenv("SANDCASTLE_TENANT", "")
	t.Setenv("SANDCASTLE_REMOTE", "")
	admin := loadAdminFromFileAndEnv(SandcastleConfig{Tenant: "file-tenant", Remote: "file-remote"})
	if admin.Tenant != "file-tenant" {
		t.Fatalf("Tenant = %q, want file-tenant", admin.Tenant)
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
	if ResolveConfigPath("nonexistent-remote") != "" {
		t.Fatal("expected empty config path for missing remote")
	}
}
