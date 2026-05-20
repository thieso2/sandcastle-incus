package dns

import (
	"fmt"
	"path"
	"strings"
)

type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    int    `json:"mode"`
}

func RenderInitial(domain string, dnsAddress string) ([]File, error) {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if dnsAddress == "" {
		return nil, fmt.Errorf("DNS address is required")
	}

	zonePath := path.Join("/etc/coredns/zones", "db."+domain)
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
			Path: zonePath,
			Mode: 0o644,
			Content: fmt.Sprintf(`$ORIGIN %s.
$TTL 60
@ IN SOA ns.%s. hostmaster.%s. 1 3600 600 604800 60
@ IN NS ns.%s.
ns IN A %s
`, domain, domain, domain, domain, dnsAddress),
		},
	}, nil
}
