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

func RenderInitial(domain string, dnsAddress string) ([]File, error) {
	return RenderProject(domain, dnsAddress, nil)
}

func RenderProject(domain string, dnsAddress string, sandboxes []meta.Sandbox) ([]File, error) {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
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
	sort.Slice(sandboxes, func(i, j int) bool {
		return sandboxes[i].Name < sandboxes[j].Name
	})
	for _, sandbox := range sandboxes {
		if sandbox.Name == "" || sandbox.PrivateIP == "" {
			continue
		}
		zone += fmt.Sprintf("%s IN A %s\n*.%s IN A %s\n", sandbox.Name, sandbox.PrivateIP, sandbox.Name, sandbox.PrivateIP)
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
