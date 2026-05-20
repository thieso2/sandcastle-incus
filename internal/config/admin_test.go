package config

import "testing"

func TestLoadAdminFromEnvDefaults(t *testing.T) {
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
	if config.ProjectPrefix != DefaultProjectPrefix {
		t.Fatalf("ProjectPrefix = %q, want %q", config.ProjectPrefix, DefaultProjectPrefix)
	}
	if config.InfrastructureProject != DefaultInfrastructureProject {
		t.Fatalf("InfrastructureProject = %q, want %q", config.InfrastructureProject, DefaultInfrastructureProject)
	}
	if config.Images.AI != DefaultAIImageAlias {
		t.Fatalf("AI image = %q, want %q", config.Images.AI, DefaultAIImageAlias)
	}
}

func TestLoadAdminFromEnvOverrides(t *testing.T) {
	t.Setenv("SANDCASTLE_REMOTE", "prod")
	t.Setenv("SANDCASTLE_STORAGE_POOL", "fast")
	t.Setenv("SANDCASTLE_CIDR_POOL", "10.99.0.0/16")
	t.Setenv("SANDCASTLE_PROJECT_PREFIX", "dev")
	t.Setenv("SANDCASTLE_INFRA_PROJECT", "dev-infra")
	t.Setenv("SANDCASTLE_INFRA_HOST", "203.0.113.10")
	t.Setenv("SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET", "/var/lib/incus/unix.socket")
	t.Setenv("SANDCASTLE_DENIED_DOMAIN_SUFFIXES", "corp.example, internal.example ")
	t.Setenv("SANDCASTLE_BASE_IMAGE", "images:debian/13")
	t.Setenv("SANDCASTLE_AI_IMAGE", "sandcastle/ai:test")

	config := LoadAdminFromEnv()
	if config.Remote != "prod" {
		t.Fatalf("Remote = %q, want prod", config.Remote)
	}
	if config.Images.Base != "images:debian/13" {
		t.Fatalf("Base image = %q", config.Images.Base)
	}
	if config.InfrastructureHost != "203.0.113.10" {
		t.Fatalf("InfrastructureHost = %q", config.InfrastructureHost)
	}
	if config.RouteBrokerIncusSocket != "/var/lib/incus/unix.socket" {
		t.Fatalf("RouteBrokerIncusSocket = %q", config.RouteBrokerIncusSocket)
	}
	if len(config.DeniedDomainSuffixes) != 2 || config.DeniedDomainSuffixes[0] != "corp.example" {
		t.Fatalf("DeniedDomainSuffixes = %#v", config.DeniedDomainSuffixes)
	}
}

func TestAdminValidateRejectsMissingRequiredValues(t *testing.T) {
	config := LoadAdminFromEnv()
	config.StoragePool = ""
	if err := config.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestAdminValidateAcceptsDefaults(t *testing.T) {
	config := LoadAdminFromEnv()
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
}
