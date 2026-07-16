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
