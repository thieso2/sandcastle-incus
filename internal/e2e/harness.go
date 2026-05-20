package e2e

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const enabledEnv = "SANDCASTLE_E2E"

type Config struct {
	Enabled       bool
	Remote        string
	StoragePool   string
	CIDRPool      string
	DomainSuffix  string
	RunID         string
	Keep          bool
	SandcastleBin string
	RouteBroker   RouteBrokerConfig
	Tailscale     TailscaleConfig
	Images        ImageConfig
}

type RouteBrokerConfig struct {
	IncusSocket string
}

type TailscaleConfig struct {
	AuthKey string
	Tag     string
}

type ImageConfig struct {
	BaseSource    string
	AISource      string
	Build         bool
	BuildTool     string
	CodexVersion  string
	ClaudeVersion string
	GeminiVersion string
}

func LoadConfig() Config {
	tag := getenv("SANDCASTLE_E2E_TAILSCALE_TAG", "tag:sandcastle")
	return Config{
		Enabled:       os.Getenv(enabledEnv) == "1",
		Remote:        getenv("SANDCASTLE_E2E_REMOTE", "local"),
		StoragePool:   getenv("SANDCASTLE_E2E_STORAGE_POOL", "default"),
		CIDRPool:      getenv("SANDCASTLE_E2E_CIDR_POOL", "10.248.0.0/16"),
		DomainSuffix:  strings.TrimPrefix(getenv("SANDCASTLE_E2E_DOMAIN_SUFFIX", "e2e.project-tld"), "."),
		RunID:         os.Getenv("SANDCASTLE_E2E_RUN_ID"),
		Keep:          os.Getenv("SANDCASTLE_E2E_KEEP") == "1",
		SandcastleBin: os.Getenv("SANDCASTLE_E2E_SANDCASTLE_BIN"),
		RouteBroker: RouteBrokerConfig{
			IncusSocket: os.Getenv("SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET"),
		},
		Tailscale: TailscaleConfig{
			AuthKey: os.Getenv("SANDCASTLE_E2E_TAILSCALE_AUTHKEY"),
			Tag:     tag,
		},
		Images: ImageConfig{
			BaseSource:    os.Getenv("SANDCASTLE_E2E_BASE_IMAGE_SOURCE"),
			AISource:      os.Getenv("SANDCASTLE_E2E_AI_IMAGE_SOURCE"),
			Build:         os.Getenv("SANDCASTLE_E2E_IMAGE_BUILD") == "1",
			BuildTool:     getenv("SANDCASTLE_E2E_IMAGE_BUILD_TOOL", "docker"),
			CodexVersion:  os.Getenv("SANDCASTLE_E2E_CODEX_VERSION"),
			ClaudeVersion: os.Getenv("SANDCASTLE_E2E_CLAUDE_CODE_VERSION"),
			GeminiVersion: os.Getenv("SANDCASTLE_E2E_GEMINI_CLI_VERSION"),
		},
	}
}

func (c Config) Validate() error {
	if !c.Enabled {
		return fmt.Errorf("set %s=1 to enable destructive e2e tests", enabledEnv)
	}
	if c.Remote == "" {
		return fmt.Errorf("SANDCASTLE_E2E_REMOTE is required")
	}
	if c.StoragePool == "" {
		return fmt.Errorf("SANDCASTLE_E2E_STORAGE_POOL is required")
	}
	if c.CIDRPool == "" {
		return fmt.Errorf("SANDCASTLE_E2E_CIDR_POOL is required")
	}
	if c.DomainSuffix == "" {
		return fmt.Errorf("SANDCASTLE_E2E_DOMAIN_SUFFIX is required")
	}
	if c.Tailscale.Tag == "" {
		return fmt.Errorf("SANDCASTLE_E2E_TAILSCALE_TAG is required when e2e is enabled")
	}
	return nil
}

func (c Config) DisposableRunID() string {
	if c.RunID != "" {
		return safeToken(c.RunID)
	}
	return "e2e-" + time.Now().UTC().Format("20060102-150405")
}

func safeToken(value string) string {
	value = strings.ToLower(value)
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
