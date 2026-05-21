package caddy

import (
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
)

func TestRenderMachine(t *testing.T) {
	file := RenderMachine("codex.acme.sandcastle.internal", 5173, "/etc/caddy/certs/tls.crt", "/etc/caddy/certs/tls.key")
	if file.Path != "/etc/caddy/Caddyfile" {
		t.Fatalf("Path = %q", file.Path)
	}
	if !strings.Contains(file.Content, "http://codex.acme.sandcastle.internal {\n    reverse_proxy 127.0.0.1:5173") {
		t.Fatalf("content missing HTTP hostname: %q", file.Content)
	}
	if !strings.Contains(file.Content, "https://codex.acme.sandcastle.internal {\n    tls /etc/caddy/certs/tls.crt /etc/caddy/certs/tls.key") {
		t.Fatalf("content missing hostname: %q", file.Content)
	}
	if !strings.Contains(file.Content, "reverse_proxy 127.0.0.1:5173") {
		t.Fatalf("content missing proxy port: %q", file.Content)
	}
	if !strings.Contains(file.Content, "auto_https disable_redirects") {
		t.Fatalf("content missing disabled automatic redirects: %q", file.Content)
	}
}

func TestRenderMachineHosts(t *testing.T) {
	file := RenderMachineHosts([]string{"codex.acme.sandcastle.internal", "example.com"}, 5173, "/etc/caddy/certs/tls.crt", "/etc/caddy/certs/tls.key")
	if !strings.Contains(file.Content, "http://codex.acme.sandcastle.internal {") || !strings.Contains(file.Content, "https://example.com {") {
		t.Fatalf("content missing hostnames: %q", file.Content)
	}
}

func TestRenderInfrastructureRoutes(t *testing.T) {
	file := RenderInfrastructure([]meta.Route{
		{Hostname: "z.example.com", TargetIP: "10.248.0.21", RoutePort: 3000},
		{Hostname: "app.example.com", TargetIP: "10.248.0.20", RoutePort: 5173},
	})
	if !strings.Contains(file.Content, "http://app.example.com {\n    reverse_proxy http://10.248.0.20:5173") {
		t.Fatalf("content missing app HTTP route: %q", file.Content)
	}
	if !strings.Contains(file.Content, "https://app.example.com {\n    reverse_proxy http://10.248.0.20:5173") {
		t.Fatalf("content missing app route: %q", file.Content)
	}
	if strings.Index(file.Content, "app.example.com") > strings.Index(file.Content, "z.example.com") {
		t.Fatalf("routes should be sorted: %q", file.Content)
	}
}

func TestRenderInfrastructureBootstrap(t *testing.T) {
	file := RenderInfrastructure(nil)
	if !strings.Contains(file.Content, `respond "sandcastle infrastructure"`) {
		t.Fatalf("content = %q", file.Content)
	}
	if !strings.Contains(file.Content, "auto_https disable_redirects") {
		t.Fatalf("content missing disabled automatic redirects: %q", file.Content)
	}
}

func TestRenderInfrastructureIncludesLetsEncryptEmail(t *testing.T) {
	file := RenderInfrastructureWithOptions([]meta.Route{
		{Hostname: "app.example.com", TargetIP: "10.248.0.20", RoutePort: 5173},
	}, InfrastructureOptions{LetsEncryptEmail: " ops@example.com "})
	if !strings.HasPrefix(file.Content, "{\n    email ops@example.com\n    auto_https disable_redirects\n}\n\n") {
		t.Fatalf("content = %q", file.Content)
	}
	if !strings.Contains(file.Content, "https://app.example.com {\n    reverse_proxy http://10.248.0.20:5173") {
		t.Fatalf("content missing app route: %q", file.Content)
	}
}
