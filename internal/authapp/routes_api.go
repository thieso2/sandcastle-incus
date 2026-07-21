package authapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	svclog "github.com/thieso2/sandcastle-incus/internal/svclog"
)

// Route API (Spec #111). The Auth App is the sole owner of Public Routes:
// token-gated POST/GET/DELETE /api/routes for Tenants, plus an unauthenticated
// GET /api/routes/ask that Caddy's on-demand TLS calls to gate certificate
// issuance to registered Hostnames only.

// RoutePublishRequest is the POST /api/routes body. Hostname (custom) takes
// precedence over Name (subdomain label); when both are empty the Machine name
// is the label. The <tenant> segment is always derived server-side, never from
// this request.
type RoutePublishRequest struct {
	Tenant      string `json:"tenant"`
	Project     string `json:"project"`
	Machine     string `json:"machine"`
	BackendPort int    `json:"backendPort"`
	Name        string `json:"name,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
}

// RouteView is the API representation of a Route.
type RouteView struct {
	Hostname    string `json:"hostname"`
	URL         string `json:"url"`
	Tenant      string `json:"tenant"`
	Project     string `json:"project"`
	Machine     string `json:"machine"`
	BackendPort int    `json:"backendPort"`
	Status      string `json:"status"`
}

// RouteListResult is the GET /api/routes response.
type RouteListResult struct {
	Routes []RouteView `json:"routes"`
}

const routesUnavailableMessage = "public routes are not available on this install: it has no route ingress. Redeploy with `--route-ingress acme` — or `--route-ingress acme-proxied` when something else already owns the host :80/:443 and an SNI proxy forwards to the appliance (sc-adm install or sc-adm auth-app deploy). Either way it runs beside a Cloudflare-tunnelled login host."

func routeView(rs RouteStatus) RouteView {
	return RouteView{
		Hostname:    rs.Hostname,
		URL:         "https://" + rs.Hostname,
		Tenant:      rs.Tenant,
		Project:     rs.Project,
		Machine:     rs.Machine,
		BackendPort: rs.BackendPort,
		Status:      rs.Status,
	}
}

func (h handler) routesAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.routePublish(w, r)
	case http.MethodGet:
		h.routeGet(w, r)
	case http.MethodDelete:
		h.routeDelete(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h handler) routePublish(w http.ResponseWriter, r *http.Request) {
	manager, ok := h.routeManager()
	if !ok {
		http.Error(w, routesUnavailableMessage, http.StatusNotImplemented)
		return
	}
	user, err := h.requireBearerUser(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var request RoutePublishRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	request.Tenant = strings.TrimSpace(request.Tenant)
	request.Project = strings.TrimSpace(request.Project)
	request.Machine = strings.TrimSpace(request.Machine)
	if err := h.authorizeWorkloadTenant(r.Context(), user.UserKey, request.Tenant); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if request.BackendPort <= 0 {
		http.Error(w, "a positive backend port is required", http.StatusBadRequest)
		return
	}
	hostname, err := h.routeHostname(request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var route Route
	err = svclog.Span(r.Context(), "route.publish", func() error {
		var pubErr error
		route, pubErr = manager.Publish(r.Context(), PublishRequest{
			Hostname:    hostname,
			Tenant:      request.Tenant,
			Project:     request.Project,
			Machine:     request.Machine,
			BackendPort: request.BackendPort,
		})
		return pubErr
	})
	if err != nil {
		var conflict *RouteConflictError
		if errors.As(err, &conflict) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, routeView(manager.Status(r.Context(), route)))
}

func (h handler) routeGet(w http.ResponseWriter, r *http.Request) {
	manager, ok := h.routeManager()
	if !ok {
		http.Error(w, routesUnavailableMessage, http.StatusNotImplemented)
		return
	}
	user, err := h.requireBearerUser(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	tenantName := strings.TrimSpace(r.URL.Query().Get("tenant"))
	hostname := normalizeHostname(r.URL.Query().Get("hostname"))

	if hostname != "" {
		route, found, err := GetRoute(r.Context(), h.db, hostname)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !found {
			http.Error(w, fmt.Sprintf("route %q is not published", hostname), http.StatusNotFound)
			return
		}
		if err := h.authorizeWorkloadTenant(r.Context(), user.UserKey, route.Tenant); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		writeJSON(w, http.StatusOK, routeView(manager.Status(r.Context(), route)))
		return
	}

	if err := h.authorizeWorkloadTenant(r.Context(), user.UserKey, tenantName); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	routes, err := ListRoutesByTenant(r.Context(), h.db, tenantName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	result := RouteListResult{Routes: []RouteView{}}
	for _, route := range routes {
		result.Routes = append(result.Routes, routeView(manager.Status(r.Context(), route)))
	}
	writeJSON(w, http.StatusOK, result)
}

func (h handler) routeDelete(w http.ResponseWriter, r *http.Request) {
	manager, ok := h.routeManager()
	if !ok {
		http.Error(w, routesUnavailableMessage, http.StatusNotImplemented)
		return
	}
	user, err := h.requireBearerUser(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	hostname := normalizeHostname(r.URL.Query().Get("hostname"))
	if hostname == "" {
		http.Error(w, "hostname is required", http.StatusBadRequest)
		return
	}
	route, found, err := GetRoute(r.Context(), h.db, hostname)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, fmt.Sprintf("route %q is not published", hostname), http.StatusNotFound)
		return
	}
	if err := h.authorizeWorkloadTenant(r.Context(), user.UserKey, route.Tenant); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	err = svclog.Span(r.Context(), "route.delete", func() error {
		return manager.Delete(r.Context(), hostname, route.Tenant)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"hostname": hostname, "status": "deleted"})
}

// routesAsk is Caddy's on-demand-TLS gate: it returns 200 only for Hostnames
// that are registered Routes, so Caddy will not fetch a certificate for an
// arbitrary Hostname under the wildcard. Unauthenticated — Caddy calls it over
// loopback.
func (h handler) routesAsk(w http.ResponseWriter, r *http.Request) {
	domain := normalizeHostname(r.URL.Query().Get("domain"))
	if domain == "" {
		http.Error(w, "domain is required", http.StatusBadRequest)
		return
	}
	// The Auth Hostname itself is a normal (non-on-demand) certificate, but allow
	// it through in case Caddy ever asks.
	if domain == h.authHostname {
		w.WriteHeader(http.StatusOK)
		return
	}
	ok, err := RouteHostnameRegistered(r.Context(), h.db, domain)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unknown host", http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// routeHostname resolves the public FQDN for a publish request: a custom
// Hostname verbatim, else <label>.<tenant>.<auth-hostname> with the label
// defaulting to the Machine name. The <tenant> segment is derived here, never
// taken from the client.
func (h handler) routeHostname(request RoutePublishRequest) (string, error) {
	if custom := normalizeHostname(request.Hostname); custom != "" {
		if !isValidPublicHostname(custom) {
			return "", fmt.Errorf("invalid custom hostname %q", request.Hostname)
		}
		return custom, nil
	}
	label := strings.TrimSpace(request.Name)
	if label == "" {
		label = request.Machine
	}
	label = strings.ToLower(strings.TrimSpace(label))
	if !isValidDNSLabel(label) {
		return "", fmt.Errorf("invalid route name %q: use letters, digits, and hyphens", label)
	}
	base := h.routeBaseDomain
	if base == "" {
		base = h.authHostname
	}
	if strings.TrimSpace(base) == "" {
		return "", fmt.Errorf("this install has no route base domain or Auth Hostname configured")
	}
	if strings.TrimSpace(request.Tenant) == "" {
		return "", fmt.Errorf("tenant is required")
	}
	return label + "." + request.Tenant + "." + base, nil
}

func (h handler) routeManager() (RouteManager, bool) {
	if h.routes == nil || h.routeCaddy == nil {
		return RouteManager{}, false
	}
	return RouteManager{
		DB:          h.db,
		Backend:     h.routes,
		Caddy:       h.routeCaddy,
		Render:      RouteRenderConfig(h.authHostname, h.authIngressMode, h.routeBaseDomain, h.acmeEmail, h.routeTLS),
		ResolveHost: h.routeResolveHost,
	}, true
}

func isValidDNSLabel(label string) bool {
	if label == "" || len(label) > 63 {
		return false
	}
	for i, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i != 0 && i != len(label)-1:
		default:
			return false
		}
	}
	return true
}

func isValidPublicHostname(hostname string) bool {
	if hostname == "" || len(hostname) > 253 || !strings.Contains(hostname, ".") {
		return false
	}
	for _, label := range strings.Split(hostname, ".") {
		if !isValidDNSLabel(label) {
			return false
		}
	}
	return true
}
