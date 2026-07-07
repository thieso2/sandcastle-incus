package localdns

import (
	"fmt"
	"net"
	"path/filepath"
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
		// A systemd-resolved drop-in: a routing-domain entry that points the
		// Tenant DNS Suffix at the tenant's CoreDNS. We deliberately do NOT use
		// `resolvectl dns/domain lo …` — systemd-resolved rejects DNS config on
		// the loopback link ("Link lo is loopback device"), and pinning it to a
		// real link (tailscale0) would clobber Tailscale's own MagicDNS servers
		// on that link. A global drop-in lets the kernel route the query to the
		// endpoint over the tailnet while leaving every other link untouched.
		//
		// The `10-` prefix is load-bearing: systemd merges resolved.conf.d into
		// one flat global server list in lexical filename order, and it does NOT
		// fall through to the next server on an authoritative NXDOMAIN. So the
		// tenant CoreDNS must sort BEFORE the public upstream (`50-…`): CoreDNS
		// answers its own zone and REFUSEs everything else (fall-through works for
		// public + other tenants), whereas a public server would NXDOMAIN a
		// tenant name first and win.
		return filepath.Join("/etc/systemd/resolved.conf.d", "10-sandcastle-"+domain+".conf")
	default:
		return filepath.Join(DefaultResolverDir(), domain)
	}
}

// SystemdResolvedDropIn renders a resolved.conf.d drop-in that routes the Tenant
// DNS Suffix to its CoreDNS endpoint. `endpoint` is host:port; the port is
// emitted only when it is not the default 53 (systemd's `DNS=IP:port` form).
// CoreDNS answers only its own zone and returns REFUSED for anything else, so
// systemd-resolved falls through to the next global server — which is why
// several tenants' drop-ins coexist without one tenant's NXDOMAIN masking
// another's names, and why public names still resolve.
func SystemdResolvedDropIn(domain string, endpoint string) (string, error) {
	domain = strings.TrimSuffix(strings.ToLower(domain), ".")
	if domain == "" {
		return "", fmt.Errorf("empty DNS suffix")
	}
	server, err := resolvedDNSServer(endpoint)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("# Managed by Sandcastle — routes *.%s to the tenant CoreDNS.\n[Resolve]\nDNS=%s\nDomains=~%s\n", domain, server, domain), nil
}

func resolvedDNSServer(endpoint string) (string, error) {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", err
	}
	if net.ParseIP(host) == nil {
		return "", fmt.Errorf("invalid DNS endpoint IP %q", host)
	}
	if port == "" || port == "53" {
		return host, nil
	}
	return net.JoinHostPort(host, port), nil
}

// resolvedReloadCommand re-reads the drop-ins after they change. A restart
// (rather than reload) is used because systemd-resolved does not re-read
// resolved.conf.d on SIGHUP; Tailscale re-pushes its per-link config afterwards,
// so MagicDNS survives the restart.
func resolvedReloadCommand() Command {
	return Command{Args: []string{"systemctl", "restart", "systemd-resolved"}}
}

func ResolverCommands(goos string, domain string, endpoint string) []Command {
	if resolverDirOverride() != "" || ResolverStrategy(goos) != StrategySystemdResolve {
		return nil
	}
	return []Command{resolvedReloadCommand()}
}

// ResolverSyncCommands reloads the platform resolver after the per-tenant
// drop-in files have been written or removed. The drop-in files themselves carry
// the per-tenant DNS routing (one file per Tenant DNS Suffix), so the only
// runtime step is to make systemd-resolved re-read them.
func ResolverSyncCommands(strategy string, state State) []Command {
	if resolverDirOverride() != "" || strategy != StrategySystemdResolve {
		return nil
	}
	return []Command{resolvedReloadCommand()}
}
