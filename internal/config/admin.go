package config

import (
	"fmt"
	"os"
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
	return Admin{
		Tenant:                 strings.TrimSpace(os.Getenv("SANDCASTLE_TENANT")),
		Project:                strings.TrimSpace(os.Getenv("SANDCASTLE_PROJECT")),
		Remote:                 getenv("SANDCASTLE_REMOTE", DefaultRemote),
		StoragePool:            getenv("SANDCASTLE_STORAGE_POOL", DefaultStoragePool),
		CIDRPool:               getenv("SANDCASTLE_CIDR_POOL", DefaultCIDRPool),
		IncusProjectPrefix:     incusProjectPrefixFromEnv(),
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
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func incusProjectPrefixFromEnv() string {
	return firstNonEmpty(os.Getenv("SANDCASTLE_INCUS_PROJECT_PREFIX"), os.Getenv("SANDCASTLE_PROJECT_PREFIX"), DefaultIncusProjectPrefix)
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
