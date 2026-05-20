package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"
)

// SandcastleConfig holds user-facing defaults stored in ~/.config/sandcastle/config.yml.
type SandcastleConfig struct {
	Owner  string `yaml:"owner,omitempty"`
	Remote string `yaml:"remote,omitempty"`
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
// It also resolves ConfigPath: if ~/.config/sandcastle/<remote>/incus/ exists, that dir is used
// so sc and sc incus both point at the same per-remote Incus config.
func LoadAdmin() Admin {
	cfg, _ := LoadSandcastleConfig(DefaultConfigPath())
	admin := loadAdminFromFileAndEnv(cfg)
	admin.ConfigPath = resolveConfigPath(admin.Remote)
	return admin
}

// loadAdminFromFileAndEnv applies env var overrides on top of a file config.
func loadAdminFromFileAndEnv(cfg SandcastleConfig) Admin {
	return Admin{
		Owner:                  firstNonEmpty(strings.TrimSpace(os.Getenv("SANDCASTLE_OWNER")), cfg.Owner),
		Remote:                 firstNonEmpty(getenv("SANDCASTLE_REMOTE", ""), cfg.Remote, DefaultRemote),
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

// resolveConfigPath returns the per-remote incus dir if it exists, otherwise empty string.
func resolveConfigPath(remote string) string {
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
