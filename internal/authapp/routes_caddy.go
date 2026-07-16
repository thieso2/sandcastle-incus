package authapp

import (
	"fmt"
	"sort"
	"strings"
)

// Ingress modes for the Auth Hostname, mirrored as plain strings so authapp need
// not import incusx. The Auth Hostname's mode governs how its Caddy site is
// rendered; it is independent of whether Public Route ingress is enabled.
const (
	IngressModeACME       = "acme"
	IngressModeCloudflare = "cloudflare"
	IngressModeNone       = "none"
)

// CaddyRenderConfig carries the inputs the Auth App needs to regenerate the
// appliance Caddyfile. It renders one Caddy config that serves BOTH the Auth
// Hostname (per its own ingress mode) AND native-ACME Public Route sites — so
// routes can run alongside a Cloudflare-tunnelled login hostname (Spec #111 +
// coexistence).
type CaddyRenderConfig struct {
	AuthHostname    string // the install's Auth Hostname (login site)
	AuthIngressMode string // how the Auth Hostname is served: acme | cloudflare | none
	RouteBaseDomain string // routes live under this (falls back to AuthHostname)
	ACMEEmail       string // Let's Encrypt contact email (optional)
	AuthAppUpstream string // where the Auth App HTTP listener is, e.g. 127.0.0.1:9444
	AskURL          string // on-demand-TLS ask endpoint, e.g. http://127.0.0.1:9444/api/routes/ask
}

// The Auth App HTTP listener and its on-demand-TLS ask endpoint are fixed at the
// container loopback (AuthAppListen = :9444). Kept as constants so the Caddy
// render config is built one way (RouteRenderConfig), not hand-assembled per
// call site.
const (
	authAppLoopbackUpstream = "127.0.0.1:9444"
	authAppAskURL           = "http://127.0.0.1:9444/api/routes/ask"
	// cloudflarePlainPort is the plain-HTTP port cloudflared dials the Auth
	// Hostname on (Cloudflare terminates TLS at the edge).
	cloudflarePlainPort = "8080"
)

// RouteRenderConfig builds the CaddyRenderConfig for Public Routes. authHostname
// + authIngressMode describe the login site; routeBaseDomain is where routes live
// (empty → the Auth Hostname). Single source for both the reconcile loop and the
// handlers.
func RouteRenderConfig(authHostname, authIngressMode, routeBaseDomain, acmeEmail string) CaddyRenderConfig {
	return CaddyRenderConfig{
		AuthHostname:    strings.Trim(strings.TrimSpace(authHostname), "."),
		AuthIngressMode: strings.TrimSpace(authIngressMode),
		RouteBaseDomain: strings.Trim(strings.TrimSpace(routeBaseDomain), "."),
		ACMEEmail:       strings.TrimSpace(acmeEmail),
		AuthAppUpstream: authAppLoopbackUpstream,
		AskURL:          authAppAskURL,
	}
}

// RenderCaddyfile produces the full appliance Caddyfile: a global block wiring
// the ACME email and the on-demand-TLS `ask` endpoint; the Auth Hostname site
// rendered per its ingress mode (a Cloudflare-tunnelled hostname is served plain
// on :8080, otherwise a normal ACME site); and one native-ACME site per Route.
// Route sites use on-demand TLS so a certificate issues lazily on first request,
// gated by the ask endpoint (registered Hostnames only). No global `auto_https
// off` — that would suppress the route certificates; the Auth Hostname stays
// cert-free instead via its explicit `http://…:8080` scheme. Routes reverse-proxy
// to the per-Route loopback port an Incus proxy device forwards to the Machine,
// so Caddy never holds a Tenant IP. Deterministic: Routes sorted by Hostname.
func RenderCaddyfile(cfg CaddyRenderConfig, routes []Route) string {
	upstream := strings.TrimSpace(cfg.AuthAppUpstream)
	if upstream == "" {
		upstream = authAppLoopbackUpstream
	}

	var b strings.Builder

	b.WriteString("{\n")
	if email := strings.TrimSpace(cfg.ACMEEmail); email != "" {
		fmt.Fprintf(&b, "\temail %s\n", email)
	}
	if ask := strings.TrimSpace(cfg.AskURL); ask != "" {
		b.WriteString("\ton_demand_tls {\n")
		fmt.Fprintf(&b, "\t\task %s\n", ask)
		b.WriteString("\t}\n")
	}
	b.WriteString("}\n\n")

	if host := strings.Trim(strings.TrimSpace(cfg.AuthHostname), "."); host != "" {
		if cfg.AuthIngressMode == IngressModeCloudflare {
			// Cloudflare terminates TLS at the edge and dials the tunnel over
			// plain HTTP — keep serving login there while routes use ACME.
			fmt.Fprintf(&b, "http://%s:%s {\n\treverse_proxy %s\n}\n", host, cloudflarePlainPort, upstream)
		} else {
			fmt.Fprintf(&b, "%s {\n\treverse_proxy %s\n}\n", host, upstream)
		}
	}

	sorted := append([]Route(nil), routes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Hostname < sorted[j].Hostname })
	for _, route := range sorted {
		host := strings.TrimSpace(route.Hostname)
		if host == "" {
			continue
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "%s {\n", host)
		b.WriteString("\ttls {\n\t\ton_demand\n\t}\n")
		fmt.Fprintf(&b, "\treverse_proxy 127.0.0.1:%d\n", route.LocalPort)
		b.WriteString("}\n")
	}

	return b.String()
}
