package localdns

import (
	"fmt"
	"hash/fnv"
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
		// A systemd unit that gives the Tenant DNS Suffix its OWN
		// systemd-resolved link scope (a dummy link carrying DNS=<CoreDNS>
		// Domains=~<suffix>). Per-domain DNS routing in systemd-resolved only
		// works ACROSS link scopes: a resolved.conf.d drop-in lands every
		// tenant's server in the one flat GLOBAL list, where resolved queries
		// only the "current server" and rotates it on failure — so with two
		// tenants the current server answers the other tenant's names with an
		// authoritative NXDOMAIN (or the rotation parks on the public
		// upstream and both zones die). A dedicated link per suffix routes
		// *.<suffix> deterministically to its own CoreDNS and keeps tenant
		// servers out of public resolution entirely.
		return filepath.Join("/etc/systemd/system", resolvedUnitName(domain))
	default:
		return filepath.Join(DefaultResolverDir(), domain)
	}
}

// resolvedUnitName is the per-suffix systemd unit carrying the link scope.
func resolvedUnitName(domain string) string {
	return "sandcastle-dns-" + strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".") + ".service"
}

// resolvedLinkName derives the dummy interface name for a suffix. Interface
// names are capped at 15 bytes and suffixes can be long or dotted, so use a
// short stable hash: scdns-<8 hex chars>.
func resolvedLinkName(domain string) string {
	return fmt.Sprintf("scdns-%08x", resolvedLinkHash(domain))
}

// resolvedLinkAddress is a deterministic link-local /32 for the dummy link.
// systemd-resolved only activates a link's DNS scope once the link carries an
// address; a 169.254/16 link-local is never routed and never conflicts with
// tenant or public traffic.
func resolvedLinkAddress(domain string) string {
	hash := resolvedLinkHash(domain)
	return fmt.Sprintf("169.254.%d.%d/32", (hash>>8)%254+1, hash%254+1)
}

func resolvedLinkHash(domain string) uint32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")))
	return hash.Sum32()
}

// legacyResolvedDropInPath is where pre-link-scope installs wrote the global
// resolved.conf.d drop-in; Install removes it so stale global servers don't
// linger next to the link scope.
func legacyResolvedDropInPath(domain string) string {
	return filepath.Join("/etc/systemd/resolved.conf.d", "10-sandcastle-"+domain+".conf")
}

// SystemdResolvedUnit renders the per-suffix unit. The unit is
// PartOf=systemd-resolved.service: per-link DNS config is runtime state that a
// resolved restart wipes, and PartOf propagates that restart here so the scope
// is re-applied automatically.
func SystemdResolvedUnit(domain string, endpoint string) (string, error) {
	domain = strings.TrimSuffix(strings.ToLower(domain), ".")
	if domain == "" {
		return "", fmt.Errorf("empty DNS suffix")
	}
	server, err := resolvedDNSServer(endpoint)
	if err != nil {
		return "", err
	}
	link := resolvedLinkName(domain)
	address := resolvedLinkAddress(domain)
	return fmt.Sprintf(`# Managed by Sandcastle — routes *.%s to the tenant CoreDNS at %s
# via a dedicated systemd-resolved link scope (dummy link %s).
[Unit]
Description=Sandcastle tenant DNS for *.%s (%s)
Wants=systemd-resolved.service
After=systemd-resolved.service network.target
PartOf=systemd-resolved.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'ip link show %s >/dev/null 2>&1 || ip link add %s type dummy'
ExecStart=/bin/sh -c 'ip link set %s up && ip addr replace %s dev %s'
ExecStart=/bin/sh -c 'resolvectl dns %s %s && resolvectl domain %s "~%s"'
ExecStop=/bin/sh -c 'ip link delete %s 2>/dev/null || true'

[Install]
WantedBy=multi-user.target
`, domain, server, link, domain, server, link, link, link, address, link, link, server, link, domain, link), nil
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

// ResolverCommands are the commands Install/Refresh run after writing the
// per-suffix unit: load it, enable it for boot, and (re)start it so a changed
// endpoint is re-applied.
func ResolverCommands(goos string, domain string, endpoint string) []Command {
	return resolverInstallCommands(ResolverStrategy(goos), domain)
}

func resolverInstallCommands(strategy string, domain string) []Command {
	if resolverDirOverride() != "" || strategy != StrategySystemdResolve {
		return nil
	}
	unit := resolvedUnitName(domain)
	return []Command{
		{Args: []string{"systemctl", "daemon-reload"}},
		{Args: []string{"systemctl", "enable", unit}},
		{Args: []string{"systemctl", "restart", unit}},
	}
}

// ResolverPreUninstallCommands run BEFORE the unit file is removed: stop the
// unit (its ExecStop deletes the dummy link and with it the resolved scope)
// and drop the boot enablement while the file still exists.
func ResolverPreUninstallCommands(strategy string, domain string) []Command {
	if resolverDirOverride() != "" || strategy != StrategySystemdResolve {
		return nil
	}
	unit := resolvedUnitName(domain)
	return []Command{
		{Args: []string{"systemctl", "disable", "--now", unit}},
	}
}

// ResolverSyncCommands reloads systemd after resolver files changed. The
// per-suffix units carry the DNS routing; the daemon just needs to re-read
// them.
func ResolverSyncCommands(strategy string, state State) []Command {
	if resolverDirOverride() != "" || strategy != StrategySystemdResolve {
		return nil
	}
	return []Command{{Args: []string{"systemctl", "daemon-reload"}}}
}
