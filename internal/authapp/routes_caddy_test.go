package authapp

import (
	"strings"
	"testing"
)

func testCaddyConfig() CaddyRenderConfig {
	return CaddyRenderConfig{
		AuthHostname:    "sc2.thieso2.dev",
		ACMEEmail:       "ops@example.dev",
		AuthAppUpstream: "127.0.0.1:9444",
		AskURL:          "http://127.0.0.1:9444/api/routes/ask",
	}
}

func TestRenderCaddyfile_GlobalAndAuthHostname(t *testing.T) {
	out := RenderCaddyfile(testCaddyConfig(), nil)
	for _, want := range []string{
		"email ops@example.dev",
		"on_demand_tls {",
		"ask http://127.0.0.1:9444/api/routes/ask",
		"sc2.thieso2.dev {",
		"reverse_proxy 127.0.0.1:9444",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Caddyfile missing %q:\n%s", want, out)
		}
	}
}

func TestRenderCaddyfile_RouteBlockUsesOnDemandAndLoopback(t *testing.T) {
	routes := []Route{
		{Hostname: "web.acme.sc2.thieso2.dev", LocalPort: 20001},
	}
	out := RenderCaddyfile(testCaddyConfig(), routes)
	if !strings.Contains(out, "web.acme.sc2.thieso2.dev {") {
		t.Errorf("missing route site:\n%s", out)
	}
	if !strings.Contains(out, "on_demand") {
		t.Errorf("route should use on-demand TLS:\n%s", out)
	}
	if !strings.Contains(out, "reverse_proxy 127.0.0.1:20001") {
		t.Errorf("route should proxy to its loopback port:\n%s", out)
	}
}

func TestRenderCaddyfile_CoexistCloudflareLoginAndAcmeRoutes(t *testing.T) {
	cfg := RouteRenderConfig("home.thieso2.dev", IngressModeCloudflare, "home.tc42.uk", "ops@example.dev", "")
	routes := []Route{{Hostname: "web.acme.home.tc42.uk", LocalPort: 20001}}
	out := RenderCaddyfile(cfg, routes)

	// Login host stays on the Cloudflare tunnel: plain HTTP on :8080, no ACME.
	if !strings.Contains(out, "http://home.thieso2.dev:8080 {") {
		t.Errorf("login host should be served plain on :8080 for cloudflare:\n%s", out)
	}
	// Route host is a native ACME on-demand site.
	if !strings.Contains(out, "web.acme.home.tc42.uk {") || !strings.Contains(out, "on_demand") {
		t.Errorf("route host should be a native ACME on-demand site:\n%s", out)
	}
	// Crucially, no global auto_https off — that would suppress the route certs.
	if strings.Contains(out, "auto_https off") {
		t.Errorf("must not disable auto_https (routes need certs):\n%s", out)
	}
	if !strings.Contains(out, "ask http://127.0.0.1:9444/api/routes/ask") {
		t.Errorf("ask endpoint missing:\n%s", out)
	}
}

func TestRenderCaddyfile_AcmeLoginHostStaysBareSite(t *testing.T) {
	cfg := RouteRenderConfig("sc2.thieso2.dev", IngressModeACME, "", "ops@example.dev", "")
	out := RenderCaddyfile(cfg, nil)
	if !strings.Contains(out, "sc2.thieso2.dev {") || strings.Contains(out, "http://sc2.thieso2.dev:8080") {
		t.Errorf("acme login host should be a bare ACME site, not :8080:\n%s", out)
	}
}

func TestRenderCaddyfile_RouteTLSInternal(t *testing.T) {
	cfg := RouteRenderConfig("sc2.thieso2.dev", IngressModeACME, "routes.test", "", RouteTLSInternal)
	routes := []Route{{Hostname: "web.acme.routes.test", LocalPort: 20001}}
	out := RenderCaddyfile(cfg, routes)
	if !strings.Contains(out, "tls internal") {
		t.Errorf("internal mode should emit `tls internal` for route sites:\n%s", out)
	}
	if strings.Contains(out, "\ttls {\n\t\ton_demand") {
		t.Errorf("internal mode must not use on-demand TLS for the route site:\n%s", out)
	}
}

func TestRenderCaddyfile_RoutesSortedDeterministic(t *testing.T) {
	routes := []Route{
		{Hostname: "b.acme.sc2.thieso2.dev", LocalPort: 20002},
		{Hostname: "a.acme.sc2.thieso2.dev", LocalPort: 20001},
	}
	out := RenderCaddyfile(testCaddyConfig(), routes)
	if strings.Index(out, "a.acme") > strings.Index(out, "b.acme") {
		t.Errorf("routes not emitted in sorted order:\n%s", out)
	}
}
