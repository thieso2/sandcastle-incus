package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"
)

// SandcastleConfig holds user-facing defaults stored in ~/.config/sandcastle/config.yml.
type SandcastleConfig struct {
	Tenant      string `yaml:"tenant,omitempty"`
	Project     string `yaml:"project,omitempty"`
	Remote      string `yaml:"remote,omitempty"`
	AdminRemote string `yaml:"admin_remote,omitempty"`
}

// DefaultConfigDir returns ~/.config/sandcastle.
func DefaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "sandcastle")
}

// DefaultConfigPath returns ~/.config/sandcastle/config.yml.
func DefaultConfigPath() string {
	return filepath.Join(DefaultConfigDir(), "config.yml")
}

// RemoteIncusDir returns the per-remote incus config directory.
func RemoteIncusDir(remoteName string) string {
	return filepath.Join(DefaultConfigDir(), remoteName, "incus")
}

// LoadSandcastleConfig reads the config file at path. Missing file returns empty config, not an error.
func LoadSandcastleConfig(path string) (SandcastleConfig, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return SandcastleConfig{}, nil
	}
	if err != nil {
		return SandcastleConfig{}, err
	}
	var cfg SandcastleConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return SandcastleConfig{}, err
	}
	return cfg, nil
}

// SaveSandcastleConfig writes cfg to path, creating parent directories as needed.
func SaveSandcastleConfig(path string, cfg SandcastleConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// LoadAdmin merges ~/.config/sandcastle/config.yml with SANDCASTLE_* env vars (env wins).
// ConfigPath is intentionally not set here — admin commands use the global ~/.config/incus/
// which holds admin certificates. User-facing commands call ResolveConfigPath separately.
func LoadAdmin() Admin {
	cfg, _ := LoadSandcastleConfig(DefaultConfigPath())
	return loadAdminFromFileAndEnv(cfg)
}

// loadAdminFromFileAndEnv applies env var overrides on top of a file config.
func loadAdminFromFileAndEnv(cfg SandcastleConfig) Admin {
	return Admin{
		Tenant:                 firstNonEmpty(strings.TrimSpace(os.Getenv("SANDCASTLE_TENANT")), cfg.Tenant),
		Project:                firstNonEmpty(strings.TrimSpace(os.Getenv("SANDCASTLE_PROJECT")), cfg.Project),
		Remote:                 firstNonEmpty(getenv("SANDCASTLE_REMOTE", ""), cfg.Remote, DefaultRemote),
		AdminRemote:            firstNonEmpty(strings.TrimSpace(os.Getenv("SANDCASTLE_ADMIN_REMOTE")), cfg.AdminRemote),
		StoragePool:            getenv("SANDCASTLE_STORAGE_POOL", DefaultStoragePool),
		CIDRPool:               getenv("SANDCASTLE_CIDR_POOL", DefaultCIDRPool),
		ProjectPrefix:          getenv("SANDCASTLE_PROJECT_PREFIX", DefaultProjectPrefix),
		InfrastructureProject:  getenv("SANDCASTLE_INFRA_PROJECT", DefaultInfrastructureProject),
		InfrastructureHost:     getenv("SANDCASTLE_INFRA_HOST", DefaultInfrastructureHost),
		LetsEncryptEmail:       getenv("SANDCASTLE_LETSENCRYPT_EMAIL", DefaultLetsEncryptEmail),
		RouteBrokerIncusSocket: strings.TrimSpace(os.Getenv("SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET")),
		AllowedDomainSuffixes:  splitList(os.Getenv("SANDCASTLE_ALLOWED_DOMAIN_SUFFIXES")),
		DeniedDomainSuffixes:   splitList(os.Getenv("SANDCASTLE_DENIED_DOMAIN_SUFFIXES")),
		Images: Images{
			Base: getenv("SANDCASTLE_BASE_IMAGE", DefaultBaseImageAlias),
			AI:   getenv("SANDCASTLE_AI_IMAGE", DefaultAIImageAlias),
		},
	}
}

// ResolveConfigPath returns the per-remote Sandcastle incus dir if it exists, otherwise empty string.
// This directory contains the restricted user TLS certificate for the remote.
func ResolveConfigPath(remote string) string {
	if remote == "" {
		return ""
	}
	dir := RemoteIncusDir(remote)
	if _, err := os.Stat(filepath.Join(dir, "config.yml")); err == nil {
		return dir
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
