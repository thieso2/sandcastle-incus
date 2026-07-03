package dns

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/meta"
)

type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    int    `json:"mode"`
}

const (
	SelfResolverPath        = "/etc/resolv.conf"
	SelfResolverContent     = "nameserver 127.0.0.1\n"
	UpstreamResolverPath    = "/etc/coredns/upstream-resolv.conf"
	UpstreamResolverContent = "nameserver 1.1.1.1\nnameserver 8.8.8.8\n"
)

func RenderInitial(suffix string, dnsAddress string, gatewayAddress string) ([]File, error) {
	return RenderTenant(suffix, dnsAddress, gatewayAddress, nil)
}

// RenderTenant builds the CoreDNS config for a tenant. The tenant zone serves
// static A records for managed machines from the zone file; the file plugin's
// fallthrough hands any name not in the zone to the bridge gateway's built-in
// dnsmasq (gatewayAddress), so freeform `incus launch` instances resolve under
// their DHCP-assigned <name>.<DefaultProjectName>.<suffix> without static records.
func RenderTenant(domain string, dnsAddress string, gatewayAddress string, machines []meta.Machine) ([]File, error) {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if domain == "" {
		return nil, fmt.Errorf("tenant DNS suffix is required")
	}
	if dnsAddress == "" {
		return nil, fmt.Errorf("DNS address is required")
	}
	if gatewayAddress == "" {
		return nil, fmt.Errorf("gateway address is required")
	}

	zonePath := path.Join("/etc/coredns/zones", "db."+domain)
	zone := fmt.Sprintf(`$ORIGIN %s.
$TTL 60
@ IN SOA ns.%s. hostmaster.%s. 1 3600 600 604800 60
@ IN NS ns.%s.
ns IN A %s
`, domain, domain, domain, domain, dnsAddress)
	sort.Slice(machines, func(i, j int) bool {
		if machines[i].Project == machines[j].Project {
			return machines[i].Name < machines[j].Name
		}
		return machines[i].Project < machines[j].Project
	})
	seenShort := map[string]bool{}
	for _, machine := range machines {
		if machine.Name == "" || machine.PrivateIP == "" {
			continue
		}
		// Short name <machine>.<suffix> — the canonical v2 tenant machine name.
		// Emitted once per name (first wins on collision across projects).
		if !seenShort[machine.Name] {
			seenShort[machine.Name] = true
			short := machine.Name + "." + domain + "."
			zone += fmt.Sprintf("%s IN A %s\n", short, machine.PrivateIP)
		}
		// Project-qualified name <machine>.<project>.<suffix> (+ wildcard) when a
		// project is known (v1 machines carry it; freeform v2 machines may not).
		if machine.Project != "" {
			record := machine.Name + "." + machine.Project + "." + domain + "."
			zone += fmt.Sprintf("%s IN A %s\n*.%s IN A %s\n", record, machine.PrivateIP, record, machine.PrivateIP)
		}
	}
	return []File{
		{
			Path: "/etc/coredns/Corefile",
			Mode: 0o644,
			Content: fmt.Sprintf(`%s:53 {
    errors
    file %s %s {
        fallthrough
    }
    forward . %s
}
.:53 {
    errors
    forward . %s {
        force_tcp
    }
}
`, domain, zonePath, domain, gatewayAddress, UpstreamResolverPath),
		},
		{
			Path:    zonePath,
			Mode:    0o644,
			Content: zone,
		},
		{
			Path:    SelfResolverPath,
			Mode:    0o644,
			Content: SelfResolverContent,
		},
		{
			Path:    UpstreamResolverPath,
			Mode:    0o644,
			Content: UpstreamResolverContent,
		},
	}, nil
}
