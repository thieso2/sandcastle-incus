package authapp

import (
	"fmt"
	"sort"
	"strings"
)

// CaddyRenderConfig carries the fixed inputs the Auth App needs to regenerate
// the appliance Caddyfile for ACME ingress. Public Routes require ACME mode
// (host :80/:443 + real Let's Encrypt certs); this renderer is ACME-only.
type CaddyRenderConfig struct {
	AuthHostname    string // the install's Auth Hostname (its own cert + site)
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
)

// RouteRenderConfig builds the CaddyRenderConfig for Public Routes from the
// install's Auth Hostname and ACME email, filling in the fixed loopback upstream
// and ask endpoint. Single source for both the reconcile loop and the handlers.
func RouteRenderConfig(authHostname, acmeEmail string) CaddyRenderConfig {
	return CaddyRenderConfig{
		AuthHostname:    strings.Trim(strings.TrimSpace(authHostname), "."),
		ACMEEmail:       strings.TrimSpace(acmeEmail),
		AuthAppUpstream: authAppLoopbackUpstream,
		AskURL:          authAppAskURL,
	}
}

// RenderCaddyfile produces the full appliance Caddyfile: a global block wiring
// the ACME email and the on-demand-TLS `ask` endpoint, the Auth Hostname site
// (normal ACME), and one site per Route. Route sites use on-demand TLS so a
// certificate is issued lazily on first request — gated by the ask endpoint,
// which only approves registered Hostnames. Routes reverse-proxy to the
// per-Route loopback port an Incus proxy device forwards to the Machine, so
// Caddy never holds a Tenant IP. Deterministic: Routes are emitted sorted by
// Hostname.
func RenderCaddyfile(cfg CaddyRenderConfig, routes []Route) string {
	upstream := strings.TrimSpace(cfg.AuthAppUpstream)
	if upstream == "" {
		upstream = "127.0.0.1:9444"
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

	if host := strings.TrimSpace(cfg.AuthHostname); host != "" {
		fmt.Fprintf(&b, "%s {\n\treverse_proxy %s\n}\n", host, upstream)
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
