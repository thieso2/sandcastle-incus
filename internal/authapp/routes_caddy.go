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

// RouteTLSInternal makes route sites use Caddy's internal self-signed CA instead
// of on-demand Let's Encrypt. It exists for hermetic e2e tests: it exercises the
// full ingress→Caddy→proxy-device→machine chain over real HTTPS without public
// DNS, inbound ports, or a real ACME server. Never set in production.
const RouteTLSInternal = "internal"

// RouteDNSProviderCloudflare enables DNS-01 for leading-wildcard Public
// Routes. Exact-host Routes deliberately retain broker-gated on-demand TLS.
const RouteDNSProviderCloudflare = "cloudflare"

const routeDNSCloudflareTokenEnv = "SANDCASTLE_ROUTE_DNS_CLOUDFLARE_API_TOKEN"

// CaddyRenderConfig carries the inputs the Auth App needs to regenerate the
// appliance Caddyfile. It renders one Caddy config that serves BOTH the Auth
// Hostname (per its own ingress mode) AND native-ACME Public Route sites — so
// routes can run alongside a Cloudflare-tunnelled login hostname (Spec #111 +
// coexistence).
type CaddyRenderConfig struct {
	AuthHostname      string   // the install's Auth Hostname (login site)
	AuthIngressMode   string   // how the Auth Hostname is served: acme | cloudflare | none
	RouteBaseDomain   string   // routes live under this (falls back to AuthHostname)
	ACMEEmail         string   // Let's Encrypt contact email (optional)
	AuthAppUpstream   string   // where the Auth App HTTP listener is, e.g. 127.0.0.1:9444
	AskURL            string   // on-demand-TLS ask endpoint, e.g. http://127.0.0.1:9444/api/routes/ask
	RouteTLS          string   // route-site TLS: "" = on-demand Let's Encrypt; "internal" = Caddy self-signed (tests)
	RouteDNSProvider  string   // "cloudflare" enables DNS-01 for explicitly authorized wildcard hostnames
	RouteDNSWildcards []string // exact leading-wildcard Route hostnames the operator authorizes for DNS-01
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
func RouteRenderConfig(authHostname, authIngressMode, routeBaseDomain, acmeEmail, routeTLS, routeDNSProvider string, routeDNSWildcards []string) CaddyRenderConfig {
	return CaddyRenderConfig{
		AuthHostname:      strings.Trim(strings.TrimSpace(authHostname), "."),
		AuthIngressMode:   strings.TrimSpace(authIngressMode),
		RouteBaseDomain:   strings.Trim(strings.TrimSpace(routeBaseDomain), "."),
		ACMEEmail:         strings.TrimSpace(acmeEmail),
		AuthAppUpstream:   authAppLoopbackUpstream,
		AskURL:            authAppAskURL,
		RouteTLS:          strings.TrimSpace(routeTLS),
		RouteDNSProvider:  strings.TrimSpace(routeDNSProvider),
		RouteDNSWildcards: normalizeRouteDNSWildcards(routeDNSWildcards),
	}
}

// RenderCaddyfile produces the full appliance Caddyfile: a global block wiring
// the ACME email and the on-demand-TLS `ask` endpoint; the Auth Hostname site
// rendered per its ingress mode (a Cloudflare-tunnelled hostname is served plain
// on :8080, otherwise a normal ACME site); and one native-ACME site per Route.
// Route sites use on-demand TLS by default; operator-configured leading-wildcard
// sites use DNS-01 instead. The on-demand path is gated by the ask endpoint
// (registered Hostnames only). No global `auto_https
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
		if cfg.RouteTLS == RouteTLSInternal {
			b.WriteString("\ttls internal\n")
		} else if routeUsesDNS01(host, cfg) {
			fmt.Fprintf(&b, "\ttls {\n\t\tdns cloudflare {env.%s}\n\t}\n", routeDNSCloudflareTokenEnv)
		} else {
			b.WriteString("\ttls {\n\t\ton_demand\n\t}\n")
		}
		fmt.Fprintf(&b, "\treverse_proxy 127.0.0.1:%d\n", route.LocalPort)
		b.WriteString("}\n")
	}

	return b.String()
}

func routeUsesDNS01(host string, cfg CaddyRenderConfig) bool {
	if cfg.RouteDNSProvider != RouteDNSProviderCloudflare || !strings.HasPrefix(host, "*.") {
		return false
	}
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	for _, allowed := range cfg.RouteDNSWildcards {
		if host == allowed {
			return true
		}
	}
	return false
}

func normalizeRouteDNSWildcards(hostnames []string) []string {
	out := make([]string, 0, len(hostnames))
	seen := map[string]bool{}
	for _, hostname := range hostnames {
		hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
		if hostname == "" || seen[hostname] {
			continue
		}
		seen[hostname] = true
		out = append(out, hostname)
	}
	return out
}
