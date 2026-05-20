package caddy

import (
	"fmt"
	"sort"

	"github.com/thieso2/sandcastle-incus/internal/meta"
)

type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    int    `json:"mode"`
}

func RenderSandbox(hostname string, appPort int, certPath string, keyPath string) File {
	return RenderSandboxHosts([]string{hostname}, appPort, certPath, keyPath)
}

func RenderSandboxHosts(hostnames []string, appPort int, certPath string, keyPath string) File {
	hosts := ""
	for i, hostname := range hostnames {
		if hostname == "" {
			continue
		}
		if hosts != "" {
			hosts += ", "
		}
		hosts += hostname
		if i == len(hostnames)-1 && hosts == "" {
			hosts = hostname
		}
	}
	return File{
		Path: "/etc/caddy/Caddyfile",
		Mode: 0o644,
		Content: fmt.Sprintf(`%s {
    tls %s %s
    reverse_proxy 127.0.0.1:%d
}
`, hosts, certPath, keyPath, appPort),
	}
}

func RenderInfrastructure(routes []meta.Route) File {
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Hostname < routes[j].Hostname
	})
	content := ""
	for _, route := range routes {
		if route.Hostname == "" || route.TargetIP == "" || route.RoutePort == 0 {
			continue
		}
		content += fmt.Sprintf(`%s {
    reverse_proxy http://%s:%d
}

`, route.Hostname, route.TargetIP, route.RoutePort)
	}
	return File{
		Path:    "/etc/caddy/Caddyfile",
		Mode:    0o644,
		Content: content,
	}
}
