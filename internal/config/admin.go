package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/naming"
)

const (
	DefaultRemote                = "local"
	DefaultStoragePool           = "default"
	DefaultCIDRPool              = "10.248.0.0/16"
	DefaultProjectPrefix         = "sc"
	DefaultInfrastructureProject = "sc-infra"
	DefaultInfrastructureHost    = ""
	DefaultLetsEncryptEmail      = ""
	DefaultBaseImageAlias        = "sandcastle/base:latest"
	DefaultAIImageAlias          = "sandcastle/ai:latest"
)

type Admin struct {
	Owner                  string
	Remote                 string
	StoragePool            string
	CIDRPool               string
	ProjectPrefix          string
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
		Owner:                  strings.TrimSpace(os.Getenv("SANDCASTLE_OWNER")),
		Remote:                 getenv("SANDCASTLE_REMOTE", DefaultRemote),
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
	if strings.TrimSpace(c.ProjectPrefix) == "" {
		return fmt.Errorf("project prefix is required")
	}
	if err := naming.ValidateProjectPrefix(c.ProjectPrefix); err != nil {
		return err
	}
	if strings.TrimSpace(c.InfrastructureProject) == "" {
		return fmt.Errorf("infrastructure project is required")
	}
	if strings.TrimSpace(c.Images.Base) == "" {
		return fmt.Errorf("base image alias is required")
	}
	if strings.TrimSpace(c.Images.AI) == "" {
		return fmt.Errorf("AI image alias is required")
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
