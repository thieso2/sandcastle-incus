package caddy

import "fmt"

type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    int    `json:"mode"`
}

func RenderSandbox(hostname string, appPort int, certPath string, keyPath string) File {
	return File{
		Path: "/etc/caddy/Caddyfile",
		Mode: 0o644,
		Content: fmt.Sprintf(`%s {
    tls %s %s
    reverse_proxy 127.0.0.1:%d
}
`, hostname, certPath, keyPath, appPort),
	}
}
