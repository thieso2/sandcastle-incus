package dns

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
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

func RenderInitial(suffix string, dnsAddress string) ([]File, error) {
	return RenderTenant(suffix, dnsAddress, nil)
}

// RenderTenant builds the CoreDNS config for a tenant. The tenant zone is the
// ONLY authority for names under the Tenant DNS Suffix (ADR-0018): there is no
// fallthrough to the bridge dnsmasq, because lease names are guest-asserted
// and single-label (they cannot express the project). Names outside the suffix
// forward upstream.
//
// Record shape per machine (ADR-0018):
//   - canonical Machine Private Hostname <machine>.<project>.<suffix> plus its
//     wildcard, for every machine whose project is known;
//   - the Default Project Short Hostname <machine>.<suffix> (plus wildcard)
//     ONLY for machines in the default project — never first-wins across
//     projects, never uniqueness-dependent;
//   - a machine with no known project (legacy callers) gets the short form only.
func RenderTenant(domain string, dnsAddress string, machines []meta.Machine) ([]File, error) {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if domain == "" {
		return nil, fmt.Errorf("tenant DNS suffix is required")
	}
	if dnsAddress == "" {
		return nil, fmt.Errorf("DNS address is required")
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
	for _, machine := range machines {
		if machine.Name == "" || machine.PrivateIP == "" {
			continue
		}
		if machine.Project == "" || machine.Project == naming.DefaultProjectName {
			short := machine.Name + "." + domain + "."
			zone += fmt.Sprintf("%s IN A %s\n*.%s IN A %s\n", short, machine.PrivateIP, short, machine.PrivateIP)
		}
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
    file %s %s
}
.:53 {
    errors
    forward . %s {
        force_tcp
    }
}
`, domain, zonePath, domain, UpstreamResolverPath),
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
