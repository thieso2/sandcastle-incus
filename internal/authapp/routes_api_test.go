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
