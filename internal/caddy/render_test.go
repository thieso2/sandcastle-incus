package caddy

import (
	"strings"
	"testing"
)

func TestRenderSandbox(t *testing.T) {
	file := RenderSandbox("codex.myproject.project-tld", 5173, "/etc/caddy/certs/tls.crt", "/etc/caddy/certs/tls.key")
	if file.Path != "/etc/caddy/Caddyfile" {
		t.Fatalf("Path = %q", file.Path)
	}
	if !strings.Contains(file.Content, "codex.myproject.project-tld") {
		t.Fatalf("content missing hostname: %q", file.Content)
	}
	if !strings.Contains(file.Content, "reverse_proxy 127.0.0.1:5173") {
		t.Fatalf("content missing proxy port: %q", file.Content)
	}
}

func TestRenderSandboxHosts(t *testing.T) {
	file := RenderSandboxHosts([]string{"codex.myproject.project-tld", "example.com"}, 5173, "/etc/caddy/certs/tls.crt", "/etc/caddy/certs/tls.key")
	if !strings.Contains(file.Content, "codex.myproject.project-tld, example.com {") {
		t.Fatalf("content missing hostnames: %q", file.Content)
	}
}
