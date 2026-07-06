package cli

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"gopkg.in/yaml.v2"
)

func normalizeAuthHostname(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimRight(value, "/")
	if value == "" {
		return ""
	}
	return value
}

func saveAuthHostnameDefault(raw string) error {
	return saveAuthDefaults(raw, "")
}

func saveAuthDefaults(rawHostname string, rawToken string) error {
	host := normalizeAuthHostname(rawHostname)
	token := strings.TrimSpace(rawToken)
	if host == "" && token == "" {
		return nil
	}
	path := scconfig.DefaultConfigPath()
	cfg, err := scconfig.LoadSandcastleConfig(path)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	changed := false
	if host != "" && cfg.AuthHostname != host {
		cfg.AuthHostname = host
		changed = true
	}
	if token != "" && cfg.AuthToken != token {
		cfg.AuthToken = token
		changed = true
	}
	if !changed {
		return nil
	}
	if err := scconfig.SaveSandcastleConfig(path, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// saveBrokerDefault records the Sandcastle Broker URL in the user config so
// broker-backed commands (`sc project create`) work without --broker.
func saveBrokerDefault(rawURL string) error {
	broker := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if broker == "" {
		return nil
	}
	path := scconfig.DefaultConfigPath()
	cfg, err := scconfig.LoadSandcastleConfig(path)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.Broker == broker {
		return nil
	}
	cfg.Broker = broker
	if err := scconfig.SaveSandcastleConfig(path, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

func commandAuthHostname(config commandConfig, override string) string {
	if host := normalizeAuthHostname(override); host != "" {
		return host
	}
	if host := normalizeAuthHostname(os.Getenv("SANDCASTLE_AUTH_HOSTNAME")); host != "" {
		return host
	}
	if host := inferAuthHostnameFromRemote(config.adminConfig.Remote); host != "" {
		return host
	}
	return normalizeAuthHostname(config.adminConfig.AuthHostname)
}

func inferAuthHostnameFromRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	incusDir := resolveIncusDir(remote)
	if incusDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(incusDir, "config.yml"))
	if err != nil {
		return ""
	}
	var cfg struct {
		Remotes map[string]struct {
			Addr string `yaml:"addr"`
		} `yaml:"remotes"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	addr := strings.TrimSpace(cfg.Remotes[remote].Addr)
	if addr == "" {
		return ""
	}
	parsed, err := url.Parse(addr)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	return parsed.Hostname()
}
