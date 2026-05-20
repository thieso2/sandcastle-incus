package localdns

import (
	"path/filepath"
)

type Command struct {
	Args []string `json:"args"`
}

const (
	StrategyMacOSResolver  = "macos-resolver"
	StrategySystemdResolve = "systemd-resolved"
	StrategyFileOnly       = "file"
)

func ResolverStrategy(goos string) string {
	switch goos {
	case "darwin":
		return StrategyMacOSResolver
	case "linux":
		return StrategySystemdResolve
	default:
		return StrategyFileOnly
	}
}

func ResolverPath(goos string, domain string) string {
	if dir := resolverDirOverride(); dir != "" {
		return filepath.Join(dir, domain)
	}
	switch ResolverStrategy(goos) {
	case StrategyMacOSResolver:
		return filepath.Join("/etc/resolver", domain)
	case StrategySystemdResolve:
		return filepath.Join("/etc/sandcastle/resolver", domain)
	default:
		return filepath.Join(DefaultResolverDir(), domain)
	}
}

func ResolverCommands(goos string, domain string, listen string) []Command {
	if ResolverStrategy(goos) != StrategySystemdResolve {
		return nil
	}
	return []Command{
		{Args: []string{"resolvectl", "dns", "lo", listen}},
		{Args: []string{"resolvectl", "domain", "lo", "~" + domain}},
	}
}
