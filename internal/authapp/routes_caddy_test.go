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
	cfg := RouteRenderConfig("home.thieso2.dev", IngressModeCloudflare, "home.tc42.uk", "ops@example.dev", "", "", nil)
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
	cfg := RouteRenderConfig("sc2.thieso2.dev", IngressModeACME, "", "ops@example.dev", "", "", nil)
	out := RenderCaddyfile(cfg, nil)
	if !strings.Contains(out, "sc2.thieso2.dev {") || strings.Contains(out, "http://sc2.thieso2.dev:8080") {
		t.Errorf("acme login host should be a bare ACME site, not :8080:\n%s", out)
	}
}

func TestRenderCaddyfile_RouteTLSInternal(t *testing.T) {
	cfg := RouteRenderConfig("sc2.thieso2.dev", IngressModeACME, "routes.test", "", RouteTLSInternal, "", nil)
	routes := []Route{{Hostname: "web.acme.routes.test", LocalPort: 20001}}
	out := RenderCaddyfile(cfg, routes)
	if !strings.Contains(out, "tls internal") {
		t.Errorf("internal mode should emit `tls internal` for route sites:\n%s", out)
	}
	if strings.Contains(out, "\ttls {\n\t\ton_demand") {
		t.Errorf("internal mode must not use on-demand TLS for the route site:\n%s", out)
	}
}

func TestRenderCaddyfile_WildcardRouteBlock(t *testing.T) {
	routes := []Route{
		{Hostname: "*.jot.moyn.dev", LocalPort: 20005},
	}
	out := RenderCaddyfile(testCaddyConfig(), routes)
	if !strings.Contains(out, "*.jot.moyn.dev {") {
		t.Errorf("missing wildcard route site:\n%s", out)
	}
	if !strings.Contains(out, "tls {\n\t\ton_demand\n\t}") {
		t.Errorf("wildcard route should use on-demand TLS:\n%s", out)
	}
	if !strings.Contains(out, "reverse_proxy 127.0.0.1:20005") {
		t.Errorf("wildcard route should proxy to its loopback port:\n%s", out)
	}
}

func TestRenderCaddyfile_WildcardRouteUsesConfiguredCloudflareDNS01(t *testing.T) {
	cfg := testCaddyConfig()
	cfg.RouteDNSProvider = RouteDNSProviderCloudflare
	cfg.RouteDNSWildcards = []string{"*.jot.moyn.dev"}
	routes := []Route{
		{Hostname: "*.jot.moyn.dev", LocalPort: 20005},
		{Hostname: "*.other.example.dev", LocalPort: 20006},
		{Hostname: "status.example.dev", LocalPort: 20007},
	}
	out := RenderCaddyfile(cfg, routes)

	wildcardStart := strings.Index(out, "*.jot.moyn.dev {")
	otherWildcardStart := strings.Index(out, "*.other.example.dev {")
	exactStart := strings.Index(out, "status.example.dev {")
	if wildcardStart < 0 || otherWildcardStart < 0 || exactStart < 0 {
		t.Fatalf("missing route blocks:\n%s", out)
	}
	wildcardBlock := out[wildcardStart:otherWildcardStart]
	if !strings.Contains(wildcardBlock, "dns cloudflare {env.SANDCASTLE_ROUTE_DNS_CLOUDFLARE_API_TOKEN}") {
		t.Errorf("wildcard route should use Cloudflare DNS-01:\n%s", wildcardBlock)
	}
	if strings.Contains(wildcardBlock, "on_demand") {
		t.Errorf("wildcard DNS-01 route must not use on-demand TLS:\n%s", wildcardBlock)
	}
	otherWildcardBlock := out[otherWildcardStart:exactStart]
	if !strings.Contains(otherWildcardBlock, "on_demand") {
		t.Errorf("wildcard route not authorized by the operator should retain on-demand TLS:\n%s", otherWildcardBlock)
	}
	exactBlock := out[exactStart:]
	if !strings.Contains(exactBlock, "on_demand") {
		t.Errorf("exact route should retain on-demand TLS:\n%s", exactBlock)
	}
}

func TestRenderCaddyfile_ExactAndWildcardBothRendered(t *testing.T) {
	routes := []Route{
		{Hostname: "foo.jot.moyn.dev", LocalPort: 20006},
		{Hostname: "*.jot.moyn.dev", LocalPort: 20007},
	}
	out := RenderCaddyfile(testCaddyConfig(), routes)
	if !strings.Contains(out, "foo.jot.moyn.dev {") {
		t.Errorf("missing exact route site:\n%s", out)
	}
	if !strings.Contains(out, "reverse_proxy 127.0.0.1:20006") {
		t.Errorf("exact route should proxy to its loopback port:\n%s", out)
	}
	if !strings.Contains(out, "*.jot.moyn.dev {") {
		t.Errorf("missing wildcard route site:\n%s", out)
	}
	if !strings.Contains(out, "reverse_proxy 127.0.0.1:20007") {
		t.Errorf("wildcard route should proxy to its loopback port:\n%s", out)
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
