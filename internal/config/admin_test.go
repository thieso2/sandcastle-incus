package config

import (
	"strings"
	"testing"
)

func TestLoadAdminFromEnvDefaults(t *testing.T) {
	clearAdminEnvForTest(t)

	config := LoadAdminFromEnv()
	if config.Remote != DefaultRemote {
		t.Fatalf("Remote = %q, want %q", config.Remote, DefaultRemote)
	}
	if config.StoragePool != DefaultStoragePool {
		t.Fatalf("StoragePool = %q, want %q", config.StoragePool, DefaultStoragePool)
	}
	if config.CIDRPool != DefaultCIDRPool {
		t.Fatalf("CIDRPool = %q, want %q", config.CIDRPool, DefaultCIDRPool)
	}
	if config.IncusProjectPrefix != DefaultIncusProjectPrefix {
		t.Fatalf("IncusProjectPrefix = %q, want %q", config.IncusProjectPrefix, DefaultIncusProjectPrefix)
	}
	if config.InfrastructureProject != DefaultInfrastructureProject {
		t.Fatalf("InfrastructureProject = %q, want %q", config.InfrastructureProject, DefaultInfrastructureProject)
	}
	if config.Images.AI != DefaultAIImageAlias {
		t.Fatalf("AI image = %q, want %q", config.Images.AI, DefaultAIImageAlias)
	}
}

func TestLoadAdminFromEnvOverridesTrimScalars(t *testing.T) {
	clearAdminEnvForTest(t)

	t.Setenv("SANDCASTLE_TENANT", " acme ")
	t.Setenv("SANDCASTLE_REMOTE", " prod ")
	t.Setenv("SANDCASTLE_STORAGE_POOL", "fast")
	t.Setenv("SANDCASTLE_CIDR_POOL", "10.99.0.0/16")
	t.Setenv("SANDCASTLE_INCUS_PROJECT_PREFIX", "dev")
	t.Setenv("SANDCASTLE_INFRA_PROJECT", "dev-infra")
	t.Setenv("SANDCASTLE_INFRA_HOST", " 203.0.113.10 ")
	t.Setenv("SANDCASTLE_LETSENCRYPT_EMAIL", " ops@example.com ")
	t.Setenv("SANDCASTLE_AUTH_HOSTNAME", " auth.example.com ")
	t.Setenv("SANDCASTLE_AUTH_GITHUB_CLIENT_ID", " github-client ")
	t.Setenv("SANDCASTLE_AUTH_GITHUB_CLIENT_SECRET", " github-secret ")
	t.Setenv("SANDCASTLE_AUTH_ADMIN_GITHUB_USERS", "OctoCat, hubot ")
	t.Setenv("SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET", " /var/lib/incus/unix.socket ")
	t.Setenv("SANDCASTLE_ALLOWED_DOMAIN_SUFFIXES", "lab.example, test ")
	t.Setenv("SANDCASTLE_DENIED_DOMAIN_SUFFIXES", "corp.example, internal.example ")
	t.Setenv("SANDCASTLE_BASE_IMAGE", " images:debian/13 ")
	t.Setenv("SANDCASTLE_AI_IMAGE", "sandcastle/ai:test")

	config := LoadAdminFromEnv()
	if config.Tenant != "acme" {
		t.Fatalf("Tenant = %q, want acme", config.Tenant)
	}
	if config.Remote != "prod" {
		t.Fatalf("Remote = %q, want prod", config.Remote)
	}
	if config.Images.Base != "images:debian/13" {
		t.Fatalf("Base image = %q", config.Images.Base)
	}
	if config.InfrastructureHost != "203.0.113.10" {
		t.Fatalf("InfrastructureHost = %q", config.InfrastructureHost)
	}
	if config.LetsEncryptEmail != "ops@example.com" {
		t.Fatalf("LetsEncryptEmail = %q", config.LetsEncryptEmail)
	}
	if config.AuthHostname != "auth.example.com" {
		t.Fatalf("AuthHostname = %q", config.AuthHostname)
	}
	if config.AuthGitHubClientID != "github-client" || config.AuthGitHubClientSecret != "github-secret" {
		t.Fatalf("GitHub OAuth config = %q/%q", config.AuthGitHubClientID, config.AuthGitHubClientSecret)
	}
	if strings.Join(config.AuthAdminGitHubUsers, ",") != "OctoCat,hubot" {
		t.Fatalf("AuthAdminGitHubUsers = %#v", config.AuthAdminGitHubUsers)
	}
	if config.RouteBrokerIncusSocket != "/var/lib/incus/unix.socket" {
		t.Fatalf("RouteBrokerIncusSocket = %q", config.RouteBrokerIncusSocket)
	}
	if len(config.AllowedDomainSuffixes) != 2 || config.AllowedDomainSuffixes[0] != "lab.example" {
		t.Fatalf("AllowedDomainSuffixes = %#v", config.AllowedDomainSuffixes)
	}
	if len(config.DeniedDomainSuffixes) != 2 || config.DeniedDomainSuffixes[0] != "corp.example" {
		t.Fatalf("DeniedDomainSuffixes = %#v", config.DeniedDomainSuffixes)
	}
}

func TestLoadAdminFromEnvPrefersIncusProjectPrefix(t *testing.T) {
	clearAdminEnvForTest(t)

	t.Setenv("SANDCASTLE_PROJECT_PREFIX", "legacy")
	t.Setenv("SANDCASTLE_INCUS_PROJECT_PREFIX", "incus")

	config := LoadAdminFromEnv()
	if config.IncusProjectPrefix != "incus" {
		t.Fatalf("IncusProjectPrefix = %q, want incus", config.IncusProjectPrefix)
	}
}

func TestLoadAdminFromEnvUsesLegacyProjectPrefixFallback(t *testing.T) {
	clearAdminEnvForTest(t)

	t.Setenv("SANDCASTLE_PROJECT_PREFIX", "legacy")

	config := LoadAdminFromEnv()
	if config.IncusProjectPrefix != "legacy" {
		t.Fatalf("IncusProjectPrefix = %q, want legacy", config.IncusProjectPrefix)
	}
}

func TestAdminValidateRejectsMissingRequiredValues(t *testing.T) {
	clearAdminEnvForTest(t)

	config := LoadAdminFromEnv()
	config.StoragePool = ""
	if err := config.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestAdminValidateRejectsInvalidIncusProjectPrefix(t *testing.T) {
	clearAdminEnvForTest(t)

	config := LoadAdminFromEnv()
	config.IncusProjectPrefix = "bad_prefix"
	if err := config.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestAdminValidateRejectsInvalidInfrastructureProject(t *testing.T) {
	clearAdminEnvForTest(t)

	config := LoadAdminFromEnv()
	config.InfrastructureProject = "bad_project"
	if err := config.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestAdminValidateRejectsInvalidDomainSuffixPolicy(t *testing.T) {
	clearAdminEnvForTest(t)

	config := LoadAdminFromEnv()
	config.AllowedDomainSuffixes = []string{"bad suffix"}
	if err := config.Validate(); err == nil {
		t.Fatal("expected error")
	}
	config = LoadAdminFromEnv()
	config.DeniedDomainSuffixes = []string{"bad/suffix"}
	if err := config.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func clearAdminEnvForTest(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"SANDCASTLE_TENANT",
		"SANDCASTLE_PROJECT",
		"SANDCASTLE_REMOTE",
		"SANDCASTLE_STORAGE_POOL",
		"SANDCASTLE_CIDR_POOL",
		"SANDCASTLE_PROJECT_PREFIX",
		"SANDCASTLE_INCUS_PROJECT_PREFIX",
		"SANDCASTLE_INFRA_PROJECT",
		"SANDCASTLE_INFRA_HOST",
		"SANDCASTLE_LETSENCRYPT_EMAIL",
		"SANDCASTLE_AUTH_HOSTNAME",
		"SANDCASTLE_AUTH_GITHUB_CLIENT_ID",
		"SANDCASTLE_AUTH_GITHUB_CLIENT_SECRET",
		"SANDCASTLE_AUTH_ADMIN_GITHUB_USERS",
		"SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET",
		"SANDCASTLE_ALLOWED_DOMAIN_SUFFIXES",
		"SANDCASTLE_DENIED_DOMAIN_SUFFIXES",
		"SANDCASTLE_BASE_IMAGE",
		"SANDCASTLE_AI_IMAGE",
	} {
		t.Setenv(key, "")
	}
}

func TestAdminValidateAcceptsDefaults(t *testing.T) {
	config := LoadAdminFromEnv()
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
}
