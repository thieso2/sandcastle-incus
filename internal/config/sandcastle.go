package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"
)

// SandcastleConfig holds user-facing defaults stored in ~/.config/sandcastle/config.yml.
type SandcastleConfig struct {
	Tenant       string `yaml:"tenant,omitempty"`
	Project      string `yaml:"project,omitempty"`
	Remote       string `yaml:"remote,omitempty"`
	AdminRemote  string `yaml:"admin_remote,omitempty"`
	AuthHostname string `yaml:"auth_hostname,omitempty"`
	AuthToken    string `yaml:"auth_token,omitempty"`
	// Broker is the Sandcastle Broker URL for tenant self-service (project
	// create). Saved by `sc login`, derived from the tenant's private CIDR.
	Broker string `yaml:"broker,omitempty"`
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

// SharedIncusDir returns the ONE incus config directory holding every
// sandcastle enrollment: a single client keypair shared across installs, and
// one remote per install (sc-<tenant>, sc-<prefix>-<tenant>) — so plain
// `incus remote switch` moves between sandcastles.
func SharedIncusDir() string {
	return filepath.Join(DefaultConfigDir(), "incus")
}

// RemoteIncusDir returns the legacy per-remote incus config directory
// (pre-shared-identity enrollments kept one dir + keypair per remote).
func RemoteIncusDir(remoteName string) string {
	return filepath.Join(DefaultConfigDir(), remoteName, "incus")
}

// remoteListedIn reports whether the incus config at dir knows the remote.
func remoteListedIn(dir string, remoteName string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "config.yml"))
	if err != nil {
		return false
	}
	var cfg struct {
		Remotes map[string]any `yaml:"remotes"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return false
	}
	_, ok := cfg.Remotes[remoteName]
	return ok
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

// LoadUser merges ~/.config/sandcastle/config.yml with exported SANDCASTLE_* env vars.
// It intentionally ignores local .env files so repo-local admin defaults do not
// redirect normal sc commands to the admin Incus remote/config.
func LoadUser() Admin {
	cfg, _ := LoadSandcastleConfig(DefaultConfigPath())
	return loadUserFromFileAndEnv(cfg)
}

// loadAdminFromFileAndEnv applies env var overrides on top of a file config.
func loadAdminFromFileAndEnv(cfg SandcastleConfig) Admin {
	env := loadAdminEnv()
	return adminFromConfigAndEnv(cfg, env)
}

func loadUserFromFileAndEnv(cfg SandcastleConfig) Admin {
	env := loadProcessEnv()
	return adminFromConfigAndEnv(cfg, env)
}

func adminFromConfigAndEnv(cfg SandcastleConfig, env map[string]string) Admin {
	return MergeAdmin(AdminDefaults(), Admin{
		Tenant:                 firstNonEmpty(strings.TrimSpace(env["SANDCASTLE_TENANT"]), cfg.Tenant),
		Project:                firstNonEmpty(strings.TrimSpace(env["SANDCASTLE_PROJECT"]), cfg.Project),
		Remote:                 firstNonEmpty(getenvFrom(env, "SANDCASTLE_REMOTE", ""), cfg.Remote),
		AdminRemote:            firstNonEmpty(strings.TrimSpace(env["SANDCASTLE_ADMIN_REMOTE"]), cfg.AdminRemote),
		StoragePool:            strings.TrimSpace(env["SANDCASTLE_STORAGE_POOL"]),
		CIDRPool:               strings.TrimSpace(env["SANDCASTLE_CIDR_POOL"]),
		IncusProjectPrefix:     incusProjectPrefixOverrideFromEnv(env),
		InfrastructureProject:  strings.TrimSpace(env["SANDCASTLE_INFRA_PROJECT"]),
		InfrastructureHost:     strings.TrimSpace(env["SANDCASTLE_INFRA_HOST"]),
		LetsEncryptEmail:       strings.TrimSpace(env["SANDCASTLE_LETSENCRYPT_EMAIL"]),
		InfrastructureTLSMode:  strings.TrimSpace(env["SANDCASTLE_INFRA_TLS_MODE"]),
		AuthHostname:           firstNonEmpty(strings.TrimSpace(env["SANDCASTLE_AUTH_HOSTNAME"]), cfg.AuthHostname),
		AuthToken:              firstNonEmpty(strings.TrimSpace(env["SANDCASTLE_AUTH_TOKEN"]), cfg.AuthToken),
		Broker:                 firstNonEmpty(strings.TrimSpace(env["SANDCASTLE_BROKER"]), cfg.Broker),
		AuthGitHubClientID:     getenvFrom(env, "SANDCASTLE_AUTH_GITHUB_CLIENT_ID", ""),
		AuthGitHubClientSecret: getenvFrom(env, "SANDCASTLE_AUTH_GITHUB_CLIENT_SECRET", ""),
		AuthAdminGitHubUsers:   splitListFrom(env, "SANDCASTLE_AUTH_ADMIN_GITHUB_USERS"),
		AuthDebugDeviceUser:    strings.TrimSpace(env["SANDCASTLE_AUTH_DEBUG_DEVICE_USER"]),
		AuthTailscaleAuthKey:   authTailscaleAuthKeyFromEnv(env),
		RouteBrokerIncusSocket: strings.TrimSpace(env["SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET"]),
		AllowedDomainSuffixes:  splitListFrom(env, "SANDCASTLE_ALLOWED_DOMAIN_SUFFIXES"),
		DeniedDomainSuffixes:   splitListFrom(env, "SANDCASTLE_DENIED_DOMAIN_SUFFIXES"),
		Images: Images{
			Base: strings.TrimSpace(env["SANDCASTLE_BASE_IMAGE"]),
			AI:   strings.TrimSpace(env["SANDCASTLE_AI_IMAGE"]),
		},
	})
}

// ResolveConfigPath returns the Sandcastle incus dir that knows the remote:
// the shared dir first (shared client identity, all installs side by side),
// then the legacy per-remote dir; empty string when the remote is unknown.
func ResolveConfigPath(remote string) string {
	if remote == "" {
		return ""
	}
	if shared := SharedIncusDir(); remoteListedIn(shared, remote) {
		return shared
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
