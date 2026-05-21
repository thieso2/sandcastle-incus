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

func RenderInitial(suffix string, dnsAddress string) ([]File, error) {
	return RenderTenant(suffix, dnsAddress, nil)
}

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
		if machine.Project == "" || machine.Name == "" || machine.PrivateIP == "" {
			continue
		}
		record := machine.Name + "." + machine.Project
		zone += fmt.Sprintf("%s IN A %s\n*.%s IN A %s\n", record, machine.PrivateIP, record, machine.PrivateIP)
	}
	return []File{
		{
			Path: "/etc/coredns/Corefile",
			Mode: 0o644,
			Content: fmt.Sprintf(`%s:53 {
    errors
    file %s %s
}
`, domain, zonePath, domain),
		},
		{
			Path:    zonePath,
			Mode:    0o644,
			Content: zone,
		},
	}, nil
}
