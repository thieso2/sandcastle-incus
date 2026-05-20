package caddy

import "fmt"

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
