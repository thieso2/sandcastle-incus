package e2e

import (
	"fmt"
	"os"
)

const enabledEnv = "SANDCASTLE_E2E"

type Config struct {
	Enabled     bool
	Remote      string
	StoragePool string
	CIDRPool    string
	RunID       string
	Keep        bool
	Tailscale   TailscaleConfig
}

type TailscaleConfig struct {
	AuthKey string
	Tag     string
}

func LoadConfig() Config {
	tag := getenv("SANDCASTLE_E2E_TAILSCALE_TAG", "tag:sandcastle")
	return Config{
		Enabled:     os.Getenv(enabledEnv) == "1",
		Remote:      getenv("SANDCASTLE_E2E_REMOTE", "local"),
		StoragePool: getenv("SANDCASTLE_E2E_STORAGE_POOL", "default"),
		CIDRPool:    getenv("SANDCASTLE_E2E_CIDR_POOL", "10.248.0.0/16"),
		RunID:       os.Getenv("SANDCASTLE_E2E_RUN_ID"),
		Keep:        os.Getenv("SANDCASTLE_E2E_KEEP") == "1",
		Tailscale: TailscaleConfig{
			AuthKey: os.Getenv("SANDCASTLE_E2E_TAILSCALE_AUTHKEY"),
			Tag:     tag,
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
	if c.Tailscale.Tag == "" {
		return fmt.Errorf("SANDCASTLE_E2E_TAILSCALE_TAG is required when e2e is enabled")
	}
	return nil
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
