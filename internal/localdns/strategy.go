package localdns

import (
	"path/filepath"
	"sort"
	"strings"
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
	if resolverDirOverride() != "" || ResolverStrategy(goos) != StrategySystemdResolve {
		return nil
	}
	return []Command{
		{Args: []string{"resolvectl", "dns", "lo", listen}},
		{Args: []string{"resolvectl", "domain", "lo", "~" + domain}},
	}
}

func ResolverSyncCommands(strategy string, state State, listen string) []Command {
	if resolverDirOverride() != "" || strategy != StrategySystemdResolve {
		return nil
	}
	domainSet := map[string]struct{}{}
	for _, project := range state.Projects {
		if _, ok := projectUpstreamEndpoint(project); !ok {
			continue
		}
		domain := strings.TrimSuffix(strings.ToLower(project.Domain), ".")
		domainSet[domain] = struct{}{}
	}
	if len(domainSet) == 0 {
		return []Command{{Args: []string{"resolvectl", "revert", "lo"}}}
	}
	domains := make([]string, 0, len(domainSet))
	for domain := range domainSet {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	domainArgs := []string{"resolvectl", "domain", "lo"}
	for _, domain := range domains {
		domainArgs = append(domainArgs, "~"+domain)
	}
	return []Command{
		{Args: []string{"resolvectl", "dns", "lo", listen}},
		{Args: domainArgs},
	}
}
