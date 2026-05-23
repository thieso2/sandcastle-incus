package caddy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/meta"
)

type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    int    `json:"mode"`
}

type InfrastructureOptions struct {
	LetsEncryptEmail string
	TLSMode          string
	AuthHostname     string
	AuthUpstream     string
	InternalRootCert string
	InternalRootKey  string
}

func RenderMachine(hostname string, appPort int, certPath string, keyPath string) File {
	return RenderMachineHosts([]string{hostname}, appPort, certPath, keyPath)
}

func RenderMachineHosts(hostnames []string, appPort int, certPath string, keyPath string) File {
	blocks := ""
	for _, hostname := range hostnames {
		if hostname == "" {
			continue
		}
		blocks += fmt.Sprintf(`http://%s {
    reverse_proxy 127.0.0.1:%d
}

https://%s {
    tls %s %s
    reverse_proxy 127.0.0.1:%d
}
`, hostname, appPort, hostname, certPath, keyPath, appPort)
	}
	return File{
		Path: "/etc/caddy/Caddyfile",
		Mode: 0o644,
		Content: fmt.Sprintf(`{
    admin 127.0.0.1:2019
    auto_https disable_redirects
}

%s`, blocks),
	}
}

func RenderInfrastructure(routes []meta.Route) File {
	return RenderInfrastructureWithOptions(routes, InfrastructureOptions{})
}

func RenderInfrastructureWithOptions(routes []meta.Route, options InfrastructureOptions) File {
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Hostname < routes[j].Hostname
	})
	content := ""
	email := strings.TrimSpace(options.LetsEncryptEmail)
	tlsDirective := infrastructureTLSDirective(options.TLSMode)
	content += "{\n"
	if email != "" {
		content += fmt.Sprintf("    email %s\n", email)
	}
	if strings.TrimSpace(options.TLSMode) == "internal" && strings.TrimSpace(options.InternalRootCert) != "" && strings.TrimSpace(options.InternalRootKey) != "" {
		content += fmt.Sprintf(`    pki {
        ca local {
            root {
                cert %s
                key %s
            }
        }
    }
`, options.InternalRootCert, options.InternalRootKey)
	}
	content += `    admin 127.0.0.1:2019
    auto_https disable_redirects
}

`
	authHostname := strings.Trim(strings.TrimSpace(options.AuthHostname), ".")
	authUpstream := strings.TrimSpace(options.AuthUpstream)
	if authHostname != "" && authUpstream != "" {
		content += fmt.Sprintf(`http://%s {
    reverse_proxy %s
}

https://%s {
%s    reverse_proxy %s
}

`, authHostname, authUpstream, authHostname, tlsDirective, authUpstream)
	}
	hasRoutes := false
	for _, route := range routes {
		if route.Hostname == "" || route.TargetIP == "" || route.RoutePort == 0 {
			continue
		}
		hasRoutes = true
		content += fmt.Sprintf(`http://%s {
    reverse_proxy http://%s:%d
}

https://%s {
%s    reverse_proxy http://%s:%d
}

`, route.Hostname, route.TargetIP, route.RoutePort, route.Hostname, tlsDirective, route.TargetIP, route.RoutePort)
	}
	if !hasRoutes {
		content += `:80 {
    respond "sandcastle infrastructure"
}
`
	}
	return File{
		Path:    "/etc/caddy/Caddyfile",
		Mode:    0o644,
		Content: content,
	}
}

func infrastructureTLSDirective(mode string) string {
	if strings.TrimSpace(mode) == "internal" {
		return "    tls internal\n"
	}
	return ""
}
