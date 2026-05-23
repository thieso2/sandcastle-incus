package config

import (
	"fmt"
	"strings"

	domainrules "github.com/thieso2/sandcastle-incus/internal/domain"
	"github.com/thieso2/sandcastle-incus/internal/naming"
)

const (
	DefaultRemote                = "local"
	DefaultStoragePool           = "default"
	DefaultCIDRPool              = "10.248.0.0/16"
	DefaultIncusProjectPrefix    = "sc"
	DefaultInfrastructureProject = "sc-infra"
	DefaultInfrastructureHost    = ""
	DefaultLetsEncryptEmail      = ""
	DefaultInfrastructureTLSMode = "acme"
	DefaultAuthHostname          = ""
	DefaultBaseImageAlias        = "sandcastle/base:latest"
	DefaultAIImageAlias          = "sandcastle/ai:latest"
)

type Admin struct {
	Tenant                 string
	Project                string
	Remote                 string
	AdminRemote            string // Incus remote for admin commands; uses global ~/.config/incus/ config
	ConfigPath             string // path to per-remote Incus config dir; empty = use default ~/.config/incus
	StoragePool            string
	CIDRPool               string
	IncusProjectPrefix     string
	InfrastructureProject  string
	InfrastructureHost     string
	LetsEncryptEmail       string
	InfrastructureTLSMode  string
	AuthHostname           string
	AuthGitHubClientID     string
	AuthGitHubClientSecret string
	AuthAdminGitHubUsers   []string
	AuthDebugDeviceUser    string
	AuthTailscaleAuthKey   string
	RouteBrokerIncusSocket string
	AllowedDomainSuffixes  []string
	DeniedDomainSuffixes   []string
	Images                 Images
}

type Images struct {
	Base string
	AI   string
}

func LoadAdminFromEnv() Admin {
	env := loadAdminEnv()
	return Admin{
		Tenant:                 strings.TrimSpace(env["SANDCASTLE_TENANT"]),
		Project:                strings.TrimSpace(env["SANDCASTLE_PROJECT"]),
		Remote:                 getenvFrom(env, "SANDCASTLE_REMOTE", DefaultRemote),
		StoragePool:            getenvFrom(env, "SANDCASTLE_STORAGE_POOL", DefaultStoragePool),
		CIDRPool:               getenvFrom(env, "SANDCASTLE_CIDR_POOL", DefaultCIDRPool),
		IncusProjectPrefix:     incusProjectPrefixFromEnv(env),
		InfrastructureProject:  getenvFrom(env, "SANDCASTLE_INFRA_PROJECT", DefaultInfrastructureProject),
		InfrastructureHost:     getenvFrom(env, "SANDCASTLE_INFRA_HOST", DefaultInfrastructureHost),
		LetsEncryptEmail:       getenvFrom(env, "SANDCASTLE_LETSENCRYPT_EMAIL", DefaultLetsEncryptEmail),
		InfrastructureTLSMode:  getenvFrom(env, "SANDCASTLE_INFRA_TLS_MODE", DefaultInfrastructureTLSMode),
		AuthHostname:           getenvFrom(env, "SANDCASTLE_AUTH_HOSTNAME", DefaultAuthHostname),
		AuthGitHubClientID:     getenvFrom(env, "SANDCASTLE_AUTH_GITHUB_CLIENT_ID", ""),
		AuthGitHubClientSecret: getenvFrom(env, "SANDCASTLE_AUTH_GITHUB_CLIENT_SECRET", ""),
		AuthAdminGitHubUsers:   splitListFrom(env, "SANDCASTLE_AUTH_ADMIN_GITHUB_USERS"),
		AuthDebugDeviceUser:    strings.TrimSpace(env["SANDCASTLE_AUTH_DEBUG_DEVICE_USER"]),
		AuthTailscaleAuthKey:   authTailscaleAuthKeyFromEnv(env),
		RouteBrokerIncusSocket: strings.TrimSpace(env["SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET"]),
		AllowedDomainSuffixes:  splitListFrom(env, "SANDCASTLE_ALLOWED_DOMAIN_SUFFIXES"),
		DeniedDomainSuffixes:   splitListFrom(env, "SANDCASTLE_DENIED_DOMAIN_SUFFIXES"),
		Images: Images{
			Base: getenvFrom(env, "SANDCASTLE_BASE_IMAGE", DefaultBaseImageAlias),
			AI:   getenvFrom(env, "SANDCASTLE_AI_IMAGE", DefaultAIImageAlias),
		},
	}
}

func authTailscaleAuthKeyFromEnv(env map[string]string) string {
	return firstNonEmpty(
		env["SANDCASTLE_AUTH_TAILSCALE_AUTHKEY"],
		env["SANDCASTLE_TAILSCALE_AUTHKEY"],
		env["SANDCASTLE_E2E_TAILSCALE_AUTHKEY"],
	)
}

func (c Admin) Validate() error {
	if strings.TrimSpace(c.Remote) == "" {
		return fmt.Errorf("remote is required")
	}
	if strings.TrimSpace(c.StoragePool) == "" {
		return fmt.Errorf("storage pool is required")
	}
	if strings.TrimSpace(c.CIDRPool) == "" {
		return fmt.Errorf("CIDR pool is required")
	}
	if strings.TrimSpace(c.IncusProjectPrefix) == "" {
		return fmt.Errorf("incus project prefix is required")
	}
	if err := naming.ValidateIncusProjectPrefix(c.IncusProjectPrefix); err != nil {
		return err
	}
	if strings.TrimSpace(c.InfrastructureProject) == "" {
		return fmt.Errorf("infrastructure project is required")
	}
	if err := naming.ValidateIncusProjectName(c.InfrastructureProject); err != nil {
		return err
	}
	if strings.TrimSpace(c.Images.Base) == "" {
		return fmt.Errorf("base image alias is required")
	}
	if strings.TrimSpace(c.Images.AI) == "" {
		return fmt.Errorf("AI image alias is required")
	}
	switch strings.TrimSpace(c.InfrastructureTLSMode) {
	case "", "acme", "internal":
	default:
		return fmt.Errorf("infrastructure TLS mode must be acme or internal")
	}
	if err := validateDomainSuffixes("allowed", c.AllowedDomainSuffixes); err != nil {
		return err
	}
	if err := validateDomainSuffixes("denied", c.DeniedDomainSuffixes); err != nil {
		return err
	}
	return nil
}

func validateDomainSuffixes(kind string, suffixes []string) error {
	for _, suffix := range suffixes {
		if strings.TrimSpace(suffix) == "" {
			continue
		}
		if _, err := domainrules.NormalizePolicySuffix(suffix); err != nil {
			return fmt.Errorf("invalid %s domain suffix %q: %w", kind, suffix, err)
		}
	}
	return nil
}

func getenv(key, fallback string) string {
	value := strings.TrimSpace(loadAdminEnv()[key])
	if value == "" {
		return fallback
	}
	return value
}

func incusProjectPrefixFromEnv(env map[string]string) string {
	return firstNonEmpty(env["SANDCASTLE_INCUS_PROJECT_PREFIX"], env["SANDCASTLE_PROJECT_PREFIX"], DefaultIncusProjectPrefix)
}

func splitList(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
