package localdns

import (
	"fmt"
	"net"
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

func ResolverCommands(goos string, domain string, endpoint string) []Command {
	if resolverDirOverride() != "" || ResolverStrategy(goos) != StrategySystemdResolve {
		return nil
	}
	return []Command{
		{Args: []string{"resolvectl", "dns", "lo", endpoint}},
		{Args: []string{"resolvectl", "domain", "lo", "~" + domain}},
	}
}

func ResolverSyncCommands(strategy string, state State) []Command {
	if resolverDirOverride() != "" || strategy != StrategySystemdResolve {
		return nil
	}
	domainSet := map[string]struct{}{}
	endpointSet := map[string]struct{}{}
	for _, tenant := range state.Tenants {
		endpoint, ok := tenantUpstreamEndpoint(tenant)
		if !ok {
			continue
		}
		domain := strings.TrimSuffix(strings.ToLower(tenant.DNSSuffix), ".")
		domainSet[domain] = struct{}{}
		endpointSet[endpoint] = struct{}{}
	}
	if len(domainSet) == 0 {
		return []Command{{Args: []string{"resolvectl", "revert", "lo"}}}
	}
	domains := make([]string, 0, len(domainSet))
	for domain := range domainSet {
		domains = append(domains, domain)
	}
	endpoints := make([]string, 0, len(endpointSet))
	for endpoint := range endpointSet {
		endpoints = append(endpoints, endpoint)
	}
	sort.Strings(domains)
	sort.Strings(endpoints)
	dnsArgs := []string{"resolvectl", "dns", "lo"}
	dnsArgs = append(dnsArgs, endpoints...)
	domainArgs := []string{"resolvectl", "domain", "lo"}
	for _, domain := range domains {
		domainArgs = append(domainArgs, "~"+domain)
	}
	return []Command{
		{Args: dnsArgs},
		{Args: domainArgs},
	}
}

func tenantUpstreamEndpoint(tenant TenantState) (string, bool) {
	domain := strings.TrimSuffix(strings.ToLower(tenant.DNSSuffix), ".")
	if domain == "" {
		return "", false
	}
	if net.ParseIP(tenant.DNSEndpoint.IP) == nil {
		return "", false
	}
	if tenant.DNSEndpoint.Port <= 0 || tenant.DNSEndpoint.Port > 65535 {
		return "", false
	}
	return net.JoinHostPort(tenant.DNSEndpoint.IP, fmt.Sprint(tenant.DNSEndpoint.Port)), true
}
