package authapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func routeTestHandler(t *testing.T, backend RouteBackend, caddy CaddyController) (http.Handler, string) {
	t.Helper()
	db := newClaimsTestDB(t)
	if err := UpsertUser(context.Background(), db, User{UserKey: "acme", GitHubUsername: "acme", Allowlisted: true}); err != nil {
		t.Fatal(err)
	}
	token, err := CreateCLIToken(context.Background(), db, "acme", timeNow())
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(db, HandlerOptions{
		AuthHostname: "sc2.thieso2.dev",
		Routes:       backend,
		RouteCaddy:   caddy,
		// Deterministic DNS in tests: custom hostnames "resolve" so status is
		// computed without a real network lookup.
		RouteResolveHost: func(context.Context, string) bool { return true },
	})
	return h, token
}

func TestRoutePublishAPI_AutoSubdomain(t *testing.T) {
	backend := newFakeBackend()
	backend.states["acme/default/web"] = running("10.248.3.42")
	caddy := &fakeCaddy{}
	h, token := routeTestHandler(t, backend, caddy)

	req := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(`{"tenant":"acme","project":"default","machine":"web","backendPort":3000}`))
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("publish = %d %q", res.Code, res.Body.String())
	}
	var view RouteView
	if err := json.Unmarshal(res.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Hostname != "web.acme.sc2.thieso2.dev" {
		t.Fatalf("hostname = %q", view.Hostname)
	}
	if view.URL != "https://web.acme.sc2.thieso2.dev" {
		t.Fatalf("url = %q", view.URL)
	}
	if view.Status != "live" {
		t.Fatalf("status = %q", view.Status)
	}
	if caddy.calls == 0 {
		t.Fatal("caddy not applied")
	}
}

func TestRoutePublishAPI_UsesRouteBaseDomain(t *testing.T) {
	// Coexistence: login on cloudflare home.thieso2.dev, routes under home.tc42.uk.
	backend := newFakeBackend()
	backend.states["acme/default/web"] = running("10.248.3.42")
	db := newClaimsTestDB(t)
	if err := UpsertUser(context.Background(), db, User{UserKey: "acme", GitHubUsername: "acme", Allowlisted: true}); err != nil {
		t.Fatal(err)
	}
	token, _ := CreateCLIToken(context.Background(), db, "acme", timeNow())
	h := NewHandler(db, HandlerOptions{
		AuthHostname:     "home.thieso2.dev",
		AuthIngressMode:  IngressModeCloudflare,
		RouteBaseDomain:  "home.tc42.uk",
		Routes:           backend,
		RouteCaddy:       &fakeCaddy{},
		RouteResolveHost: func(context.Context, string) bool { return true },
	})
	req := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(`{"tenant":"acme","project":"default","machine":"web","backendPort":3000}`))
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("publish = %d %q", res.Code, res.Body.String())
	}
	var view RouteView
	json.Unmarshal(res.Body.Bytes(), &view)
	if view.Hostname != "web.acme.home.tc42.uk" {
		t.Fatalf("route should be under the route base domain, got %q", view.Hostname)
	}
}

func TestRoutePublishAPI_CustomHostname(t *testing.T) {
	backend := newFakeBackend()
	backend.states["acme/default/web"] = running("10.248.3.42")
	h, token := routeTestHandler(t, backend, &fakeCaddy{})

	req := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(`{"tenant":"acme","project":"default","machine":"web","backendPort":3000,"hostname":"app.customer.com"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("publish custom = %d %q", res.Code, res.Body.String())
	}
	var view RouteView
	json.Unmarshal(res.Body.Bytes(), &view)
	if view.Hostname != "app.customer.com" {
		t.Fatalf("custom hostname = %q", view.Hostname)
	}
}

func TestRoutePublishAPI_UnavailableWithoutIngress(t *testing.T) {
	// No Routes/RouteCaddy wired => routes unavailable => 501.
	db := newClaimsTestDB(t)
	UpsertUser(context.Background(), db, User{UserKey: "acme", GitHubUsername: "acme", Allowlisted: true})
	token, _ := CreateCLIToken(context.Background(), db, "acme", timeNow())
	h := NewHandler(db, HandlerOptions{AuthHostname: "sc2.thieso2.dev"})

	req := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(`{"tenant":"acme","project":"default","machine":"web","backendPort":3000}`))
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 without ingress, got %d %q", res.Code, res.Body.String())
	}
}

func TestRoutePublishAPI_RejectsCrossTenant(t *testing.T) {
	backend := newFakeBackend()
	backend.states["acme/default/web"] = running("10.248.3.42")
	h, token := routeTestHandler(t, backend, &fakeCaddy{})

	// User "acme" tries to publish under tenant "globex" — no tenantAccess wired,
	// so authorization must fail.
	req := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(`{"tenant":"globex","project":"default","machine":"web","backendPort":3000}`))
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403 cross-tenant, got %d %q", res.Code, res.Body.String())
	}
}

func TestRouteAskAPI_GatesUnregisteredHostnames(t *testing.T) {
	backend := newFakeBackend()
	backend.states["acme/default/web"] = running("10.248.3.42")
	h, token := routeTestHandler(t, backend, &fakeCaddy{})

	pub := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(`{"tenant":"acme","project":"default","machine":"web","backendPort":3000}`))
	pub.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(httptest.NewRecorder(), pub)

	ok := httptest.NewRequest(http.MethodGet, "/api/routes/ask?domain=web.acme.sc2.thieso2.dev", nil)
	okRes := httptest.NewRecorder()
	h.ServeHTTP(okRes, ok)
	if okRes.Code != http.StatusOK {
		t.Fatalf("ask for registered host = %d", okRes.Code)
	}

	deny := httptest.NewRequest(http.MethodGet, "/api/routes/ask?domain=evil.acme.sc2.thieso2.dev", nil)
	denyRes := httptest.NewRecorder()
	h.ServeHTTP(denyRes, deny)
	if denyRes.Code == http.StatusOK {
		t.Fatalf("ask for unregistered host must not be 200, got %d", denyRes.Code)
	}
}

func TestIsValidDNSLabel_RejectsWildcardCharacter(t *testing.T) {
	if isValidDNSLabel("*") {
		t.Error(`isValidDNSLabel("*") should be false: "*" is not a valid DNS label character`)
	}
}

func TestIsValidPublicHostname_Wildcard(t *testing.T) {
	for _, tc := range []struct {
		name string
		host string
		want bool
	}{
		{"leading wildcard label is valid", "*.jot.moyn.dev", true},
		{"wildcard not exactly the leftmost label", "*foo.jot.moyn.dev", false},
		{"wildcard not leftmost", "foo.*.jot.moyn.dev", false},
		{"remainder has no dot", "*.dev", false},
		{"wildcard alone", "*", false},
		{"double wildcard", "*.*.jot.moyn.dev", false},
		{"plain hostname unaffected", "web.acme.sc2.thieso2.dev", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isValidPublicHostname(tc.host); got != tc.want {
				t.Errorf("isValidPublicHostname(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestRouteHostname_AcceptsWildcardCustomHostname(t *testing.T) {
	h := handler{authHostname: "sc2.thieso2.dev"}
	got, err := h.routeHostname(RoutePublishRequest{Hostname: "*.jot.moyn.dev"})
	if err != nil {
		t.Fatalf("routeHostname: %v", err)
	}
	if got != "*.jot.moyn.dev" {
		t.Errorf("routeHostname = %q, want %q", got, "*.jot.moyn.dev")
	}
}

func TestRouteCoveringWildcard(t *testing.T) {
	for _, tc := range []struct {
		domain       string
		wantWildcard string
		wantOK       bool
	}{
		{"foo123.jot.moyn.dev", "*.jot.moyn.dev", true},
		{"a.b.jot.moyn.dev", "*.b.jot.moyn.dev", true},
		{"jot.moyn.dev", "*.moyn.dev", true},
		{"solo", "", false},
	} {
		t.Run(tc.domain, func(t *testing.T) {
			gotWildcard, gotOK := routeCoveringWildcard(tc.domain)
			if gotOK != tc.wantOK || gotWildcard != tc.wantWildcard {
				t.Errorf("routeCoveringWildcard(%q) = (%q, %v), want (%q, %v)", tc.domain, gotWildcard, gotOK, tc.wantWildcard, tc.wantOK)
			}
		})
	}
}

// A wildcard Route (e.g. "*.jot.moyn.dev") covers every direct subdomain, but
// not the wildcard root itself, not a second-level subdomain, not an
// unrelated domain, and never a literal "*" SNI — Caddy never sees one.
func TestRouteAskAPI_WildcardCoverage(t *testing.T) {
	backend := newFakeBackend()
	backend.states["acme/default/jot"] = running("10.248.3.42")
	h, token := routeTestHandler(t, backend, &fakeCaddy{})

	pub := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(`{"tenant":"acme","project":"default","machine":"jot","backendPort":3000,"hostname":"*.jot.moyn.dev"}`))
	pub.Header.Set("Authorization", "Bearer "+token)
	pubRes := httptest.NewRecorder()
	h.ServeHTTP(pubRes, pub)
	if pubRes.Code != http.StatusOK {
		t.Fatalf("publish wildcard route = %d %q", pubRes.Code, pubRes.Body.String())
	}

	for _, tc := range []struct {
		domain string
		want   int
	}{
		{"foo123.jot.moyn.dev", http.StatusOK},
		{"bar.jot.moyn.dev", http.StatusOK},
		{"jot.moyn.dev", http.StatusForbidden},
		{"a.b.jot.moyn.dev", http.StatusForbidden},
		{"evil.example.com", http.StatusForbidden},
		{"*.jot.moyn.dev", http.StatusForbidden},
	} {
		t.Run(tc.domain, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/routes/ask?domain="+tc.domain, nil)
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Errorf("ask domain=%q = %d, want %d", tc.domain, res.Code, tc.want)
			}
		})
	}

	// An exactly-registered hostname that is also wildcard-covered must still
	// resolve via the exact match — exact-beats-wildcard precedence.
	pubExact := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(`{"tenant":"acme","project":"default","machine":"jot","backendPort":3000,"hostname":"pinned.jot.moyn.dev"}`))
	pubExact.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(httptest.NewRecorder(), pubExact)

	exactReq := httptest.NewRequest(http.MethodGet, "/api/routes/ask?domain=pinned.jot.moyn.dev", nil)
	exactRes := httptest.NewRecorder()
	h.ServeHTTP(exactRes, exactReq)
	if exactRes.Code != http.StatusOK {
		t.Errorf("ask for exactly-registered, wildcard-covered host = %d, want 200", exactRes.Code)
	}
}

func TestRouteListAndDeleteAPI(t *testing.T) {
	backend := newFakeBackend()
	backend.states["acme/default/web"] = running("10.248.3.42")
	h, token := routeTestHandler(t, backend, &fakeCaddy{})

	pub := httptest.NewRequest(http.MethodPost, "/api/routes", strings.NewReader(`{"tenant":"acme","project":"default","machine":"web","backendPort":3000}`))
	pub.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(httptest.NewRecorder(), pub)

	list := httptest.NewRequest(http.MethodGet, "/api/routes?tenant=acme", nil)
	list.Header.Set("Authorization", "Bearer "+token)
	listRes := httptest.NewRecorder()
	h.ServeHTTP(listRes, list)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list = %d %q", listRes.Code, listRes.Body.String())
	}
	var result RouteListResult
	json.Unmarshal(listRes.Body.Bytes(), &result)
	if len(result.Routes) != 1 || result.Routes[0].Hostname != "web.acme.sc2.thieso2.dev" {
		t.Fatalf("list result = %+v", result.Routes)
	}

	del := httptest.NewRequest(http.MethodDelete, "/api/routes?hostname=web.acme.sc2.thieso2.dev", nil)
	del.Header.Set("Authorization", "Bearer "+token)
	delRes := httptest.NewRecorder()
	h.ServeHTTP(delRes, del)
	if delRes.Code != http.StatusOK {
		t.Fatalf("delete = %d %q", delRes.Code, delRes.Body.String())
	}
	if backend.removed == 0 {
		t.Fatal("delete did not remove the proxy device")
	}
}

// The CNAME target a Tenant is told to use must never be guessed wrong: a
// Cloudflare-tunnelled Auth Hostname carries login only, so pointing a route
// hostname at it would produce a name that resolves but never serves.
func TestRouteCNAMETarget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler handler
		want    string
	}{
		{
			name:    "explicit target wins",
			handler: handler{routeCNAME: "edge.example.dev", authHostname: "auth.example.dev", authIngressMode: IngressModeACME},
			want:    "edge.example.dev",
		},
		{
			name:    "acme auth hostname is inferable",
			handler: handler{authHostname: "auth.example.dev", authIngressMode: IngressModeACME},
			want:    "auth.example.dev",
		},
		{
			name:    "cloudflare auth hostname is not a route front door",
			handler: handler{authHostname: "auth.example.dev", authIngressMode: IngressModeCloudflare},
			want:    "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.handler.routeCNAMETarget(); got != tc.want {
				t.Errorf("routeCNAMETarget() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Auto-subdomains hang off the route base domain, falling back to the Auth
// Hostname — the same rule routeHostname applies when publishing.
func TestRouteBaseFallsBackToAuthHostname(t *testing.T) {
	if got := (handler{authHostname: "auth.example.dev"}).routeBase(); got != "auth.example.dev" {
		t.Errorf("routeBase() = %q, want the auth hostname", got)
	}
	if got := (handler{authHostname: "auth.example.dev", routeBaseDomain: "routes.example.dev"}).routeBase(); got != "routes.example.dev" {
		t.Errorf("routeBase() = %q, want the configured base domain", got)
	}
}

// A Sandcastle Admin operating an install must be able to publish and pull
// Routes for any Tenant — otherwise the only cross-tenant path is editing the
// front door by hand, which is exactly what Public Routes replace.
func TestAuthorizeRouteTenantAdmitsSandcastleAdmin(t *testing.T) {
	h := handler{}
	if err := h.authorizeRouteTenant(context.Background(), User{UserKey: "thieso2", SandcastleAdmin: true}, "skorfmann"); err != nil {
		t.Fatalf("admin must reach another tenant's routes: %v", err)
	}
	if err := h.authorizeRouteTenant(context.Background(), User{UserKey: "thieso2", SandcastleAdmin: true}, ""); err == nil {
		t.Error("an empty tenant must still be rejected, even for an admin")
	}
	// A non-admin falls through to the tenant-access check, which with no
	// stores wired reports "not authorized" rather than silently allowing.
	if err := h.authorizeRouteTenant(context.Background(), User{UserKey: "thieso2"}, "skorfmann"); err == nil {
		t.Error("a non-admin must not reach another tenant's routes")
	}
	// ...and still reaches their own.
	if err := h.authorizeRouteTenant(context.Background(), User{UserKey: "thieso2"}, "thieso2"); err != nil {
		t.Errorf("a tenant must still reach their own routes: %v", err)
	}
}
