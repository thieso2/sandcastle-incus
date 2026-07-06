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
	if cfg.Tenant != "" || cfg.Project != "" || cfg.Remote != "" || cfg.AuthHostname != "" || cfg.AuthToken != "" {
		t.Fatalf("expected empty config, got %+v", cfg)
	}
}

func TestLoadSandcastleConfigReadsTenantProjectAndRemote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("tenant: acme\nproject: website\nremote: sandcastle-acme\nauth_hostname: big.example.dev\nauth_token: token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadSandcastleConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tenant != "acme" || cfg.Project != "website" || cfg.Remote != "sandcastle-acme" || cfg.AuthHostname != "big.example.dev" || cfg.AuthToken != "token" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestSaveAndReloadSandcastleConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := SaveSandcastleConfig(path, SandcastleConfig{Tenant: "acme", Project: "website", Remote: "prod", AuthHostname: "big.example.dev", AuthToken: "token"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadSandcastleConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tenant != "acme" || cfg.Project != "website" || cfg.Remote != "prod" || cfg.AuthHostname != "big.example.dev" || cfg.AuthToken != "token" {
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

func TestLoadUserFromFileAndEnvIgnoresDotEnvDefaults(t *testing.T) {
	clearAdminEnvForTest(t)
	dir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	})
	if err := os.WriteFile(".env", []byte("SANDCASTLE_REMOTE=admin-remote\nSANDCASTLE_TENANT=admin-tenant\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	admin := loadUserFromFileAndEnv(SandcastleConfig{Tenant: "user-tenant", Remote: "user-remote"})
	if admin.Tenant != "user-tenant" {
		t.Fatalf("Tenant = %q, want user-tenant", admin.Tenant)
	}
	if admin.Remote != "user-remote" {
		t.Fatalf("Remote = %q, want user-remote", admin.Remote)
	}
}

func TestLoadUserFromFileAndEnvExportedEnvWins(t *testing.T) {
	clearAdminEnvForTest(t)
	t.Setenv("SANDCASTLE_REMOTE", "env-remote")
	admin := loadUserFromFileAndEnv(SandcastleConfig{Remote: "file-remote"})
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

func TestLoadAdminFromFileAndEnvUsesAuthHostname(t *testing.T) {
	t.Setenv("SANDCASTLE_AUTH_HOSTNAME", "")
	admin := loadAdminFromFileAndEnv(SandcastleConfig{AuthHostname: "big.example.dev"})
	if admin.AuthHostname != "big.example.dev" {
		t.Fatalf("AuthHostname = %q, want big.example.dev", admin.AuthHostname)
	}
	t.Setenv("SANDCASTLE_AUTH_HOSTNAME", "auth.example.dev")
	admin = loadAdminFromFileAndEnv(SandcastleConfig{AuthHostname: "big.example.dev"})
	if admin.AuthHostname != "auth.example.dev" {
		t.Fatalf("AuthHostname = %q, want auth.example.dev", admin.AuthHostname)
	}
}

func TestLoadAdminFromFileAndEnvUsesAuthToken(t *testing.T) {
	t.Setenv("SANDCASTLE_AUTH_TOKEN", "")
	admin := loadAdminFromFileAndEnv(SandcastleConfig{AuthToken: "file-token"})
	if admin.AuthToken != "file-token" {
		t.Fatalf("AuthToken = %q, want file-token", admin.AuthToken)
	}
	t.Setenv("SANDCASTLE_AUTH_TOKEN", "env-token")
	admin = loadAdminFromFileAndEnv(SandcastleConfig{AuthToken: "file-token"})
	if admin.AuthToken != "env-token" {
		t.Fatalf("AuthToken = %q, want env-token", admin.AuthToken)
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

// Shared-identity resolution: the shared dir wins when it knows the remote;
// legacy per-remote dirs still resolve for old enrollments.
func TestResolveConfigPathSharedFirst(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	shared := SharedIncusDir()
	if err := os.MkdirAll(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "config.yml"), []byte("remotes:\n  sc-acme:\n    addr: https://x:8443\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := RemoteIncusDir("sandcastle-old")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "config.yml"), []byte("remotes: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ResolveConfigPath("sc-acme"); got != shared {
		t.Fatalf("sc-acme -> %q, want shared dir %q", got, shared)
	}
	if got := ResolveConfigPath("sandcastle-old"); got != legacy {
		t.Fatalf("sandcastle-old -> %q, want legacy dir %q", got, legacy)
	}
	if got := ResolveConfigPath("unknown"); got != "" {
		t.Fatalf("unknown -> %q, want empty", got)
	}
}
