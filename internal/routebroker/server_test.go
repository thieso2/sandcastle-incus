package routebroker

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/route"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type fakeBrokerRoutes struct {
	added   *route.AddPlan
	removed *route.RemovePlan
	list    route.ListResult
	addErr  error
	rmErr   error
}

func (r *fakeBrokerRoutes) Add(ctx context.Context, plan route.AddPlan) error {
	r.added = &plan
	return r.addErr
}

func (r *fakeBrokerRoutes) Remove(ctx context.Context, plan route.RemovePlan) error {
	r.removed = &plan
	return r.rmErr
}

func (r *fakeBrokerRoutes) List(ctx context.Context, plan route.ListPlan) (route.ListResult, error) {
	return r.list, nil
}

type fakeBrokerMachineStore struct{}

func (s fakeBrokerMachineStore) FindMachine(ctx context.Context, summary project.Summary, projectName string, machineName string) (meta.Machine, error) {
	return meta.Machine{
		Tenant:    summary.Tenant,
		Project:   projectName,
		Name:      machineName,
		AppPort:   5173,
		PrivateIP: "10.248.0.20",
	}, nil
}

type fakeBrokerMetadata struct {
	route meta.Route
}

func (s fakeBrokerMetadata) FindRoute(ctx context.Context, hostname string) (meta.Route, error) {
	return s.route, nil
}

type fakeBrokerDNSResolver struct {
	hosts []string
	err   error
}

func (r fakeBrokerDNSResolver) LookupHost(ctx context.Context, hostname string) ([]string, error) {
	return r.hosts, r.err
}

func TestServerAddsAuthorizedRoute(t *testing.T) {
	routes := &fakeBrokerRoutes{}
	server := brokerServerForTest(t, routes, fakeBrokerMetadata{})
	response := httptest.NewRecorder()
	request := brokerRequest(t, http.MethodPost, "/routes", `{"hostname":"app.example.com","targetReference":"acme/default/codex"}`)
	server.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if routes.added == nil {
		t.Fatal("expected route add")
	}
	if routes.added.RoutePort != 5173 {
		t.Fatalf("RoutePort = %d", routes.added.RoutePort)
	}
	if len(routes.added.DNSProof.ResolvedTargets) != 1 || routes.added.DNSProof.ResolvedTargets[0] != "203.0.113.10" {
		t.Fatalf("DNSProof.ResolvedTargets = %#v", routes.added.DNSProof.ResolvedTargets)
	}
	routeMetadata, err := meta.ParseRouteConfig(routes.added.MetadataConfig)
	if err != nil {
		t.Fatal(err)
	}
	if routeMetadata.CreatedBy != "alice" {
		t.Fatalf("CreatedBy = %q", routeMetadata.CreatedBy)
	}
}

func TestServerRejectsRouteAddWhenDNSProofFails(t *testing.T) {
	routes := &fakeBrokerRoutes{}
	server := brokerServerForTest(t, routes, fakeBrokerMetadata{})
	server.Resolver = fakeBrokerDNSResolver{hosts: []string{"203.0.113.11"}}
	response := httptest.NewRecorder()
	request := brokerRequest(t, http.MethodPost, "/routes", `{"hostname":"app.example.com","targetReference":"acme/default/codex"}`)
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if routes.added != nil {
		t.Fatal("route should not be added")
	}
	if errorText := decodeError(t, response); !strings.Contains(errorText, "want 203.0.113.10") {
		t.Fatalf("error = %q", errorText)
	}
}

func TestServerAllowsRouteAddForAnyUserGrantedTenant(t *testing.T) {
	routes := &fakeBrokerRoutes{}
	server := brokerServerForTest(t, routes, fakeBrokerMetadata{})
	server.Trust = fakeTrustMapper{principal: Principal{Owner: "bob", Projects: []string{"sc-acme"}}}
	response := httptest.NewRecorder()
	request := brokerRequest(t, http.MethodPost, "/routes", `{"hostname":"app.example.com","targetReference":"acme/default/codex"}`)
	server.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if routes.added == nil {
		t.Fatal("expected route add")
	}
}

func TestServerRejectsRouteAddOutsideCertificateTenantScope(t *testing.T) {
	routes := &fakeBrokerRoutes{}
	server := brokerServerForTest(t, routes, fakeBrokerMetadata{})
	server.Trust = fakeTrustMapper{principal: Principal{Owner: "alice", Projects: []string{"sc-other"}}}
	response := httptest.NewRecorder()
	request := brokerRequest(t, http.MethodPost, "/routes", `{"hostname":"app.example.com","targetReference":"acme/default/codex"}`)
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if routes.added != nil {
		t.Fatal("route should not be added")
	}
}

func TestServerReturnsConflictForClaimedRouteAdd(t *testing.T) {
	routes := &fakeBrokerRoutes{addErr: route.NewConflictError("public route hostname app.example.com is already claimed by bob/other/web")}
	server := brokerServerForTest(t, routes, fakeBrokerMetadata{})
	response := httptest.NewRecorder()
	request := brokerRequest(t, http.MethodPost, "/routes", `{"hostname":"app.example.com","targetReference":"acme/default/codex"}`)
	server.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if errorText := decodeError(t, response); errorText != "public route hostname app.example.com is already claimed by bob/other/web" {
		t.Fatalf("error = %q", errorText)
	}
}

func TestServerRejectsRouteAddWithUnknownFields(t *testing.T) {
	routes := &fakeBrokerRoutes{}
	server := brokerServerForTest(t, routes, fakeBrokerMetadata{})
	response := httptest.NewRecorder()
	request := brokerRequest(t, http.MethodPost, "/routes", `{"hostname":"app.example.com","targetReference":"acme/default/codex","admin":true}`)
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if routes.added != nil {
		t.Fatal("route should not be added")
	}
	if errorText := decodeError(t, response); !bytes.Contains([]byte(errorText), []byte("unknown field")) {
		t.Fatalf("error = %q", errorText)
	}
}

func TestServerRejectsOversizedRouteAddRequest(t *testing.T) {
	routes := &fakeBrokerRoutes{}
	server := brokerServerForTest(t, routes, fakeBrokerMetadata{})
	response := httptest.NewRecorder()
	request := brokerRequest(t, http.MethodPost, "/routes", `{"hostname":"`+strings.Repeat("x", maxAddRequestBytes)+`.example.com","targetReference":"acme/default/codex"}`)
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if routes.added != nil {
		t.Fatal("route should not be added")
	}
	if errorText := decodeError(t, response); !strings.Contains(errorText, "too large") {
		t.Fatalf("error = %q", errorText)
	}
}

func TestServerRemovesAuthorizedRoute(t *testing.T) {
	routes := &fakeBrokerRoutes{}
	server := brokerServerForTest(t, routes, fakeBrokerMetadata{route: meta.Route{
		Hostname:      "app.example.com",
		TargetTenant:  "acme",
		TargetProject: "default",
		TargetMachine: "codex",
	}})
	response := httptest.NewRecorder()
	request := brokerRequest(t, http.MethodDelete, "/routes/app.example.com", "")
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if routes.removed == nil || routes.removed.Hostname != "app.example.com" {
		t.Fatalf("removed = %#v", routes.removed)
	}
}

func TestServerRejectsRouteRemoveOutsideCertificateTenantScope(t *testing.T) {
	routes := &fakeBrokerRoutes{}
	server := brokerServerForTest(t, routes, fakeBrokerMetadata{route: meta.Route{
		Hostname:      "app.example.com",
		TargetTenant:  "acme",
		TargetProject: "default",
		TargetMachine: "codex",
	}})
	server.Trust = fakeTrustMapper{principal: Principal{Owner: "alice", Projects: []string{"sc-other"}}}
	response := httptest.NewRecorder()
	request := brokerRequest(t, http.MethodDelete, "/routes/app.example.com", "")
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if routes.removed != nil {
		t.Fatal("route should not be removed")
	}
}

func TestServerDecodesRemoveRouteHostname(t *testing.T) {
	routes := &fakeBrokerRoutes{}
	metadata := &recordingBrokerMetadata{route: meta.Route{
		Hostname:      "app-test.example.com",
		TargetTenant:  "acme",
		TargetProject: "default",
		TargetMachine: "codex",
	}}
	server := brokerServerForTest(t, routes, metadata)
	response := httptest.NewRecorder()
	request := brokerRequest(t, http.MethodDelete, "/routes/app%2dtest.example.com", "")
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if metadata.hostname != "app-test.example.com" {
		t.Fatalf("metadata lookup hostname = %q", metadata.hostname)
	}
	if routes.removed == nil || routes.removed.Hostname != "app-test.example.com" {
		t.Fatalf("removed = %#v", routes.removed)
	}
}

func TestServerNormalizesRemoveRouteHostnameBeforeLookup(t *testing.T) {
	routes := &fakeBrokerRoutes{}
	metadata := &recordingBrokerMetadata{route: meta.Route{
		Hostname:      "app.example.com",
		TargetTenant:  "acme",
		TargetProject: "default",
		TargetMachine: "codex",
	}}
	server := brokerServerForTest(t, routes, metadata)
	response := httptest.NewRecorder()
	request := brokerRequest(t, http.MethodDelete, "/routes/App.Example.COM.", "")
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if metadata.hostname != "app.example.com" {
		t.Fatalf("metadata lookup hostname = %q", metadata.hostname)
	}
	if routes.removed == nil || routes.removed.Hostname != "app.example.com" {
		t.Fatalf("removed = %#v", routes.removed)
	}
}

func TestServerListsOnlyPrincipalRoutes(t *testing.T) {
	routes := &fakeBrokerRoutes{list: route.ListResult{Routes: []route.Route{
		{Hostname: "app.example.com", TargetReference: "acme/default/codex", RoutePort: 3000},
		{Hostname: "other-project.example.com", TargetReference: "acme/other/codex", RoutePort: 3000},
		{Hostname: "missing-machine.example.com", TargetReference: "acme/default", RoutePort: 3000},
		{Hostname: "invalid-machine.example.com", TargetReference: "acme/default/bad_name", RoutePort: 3000},
		{Hostname: "other.example.com", TargetReference: "other/default/codex", RoutePort: 3000},
	}}}
	server := brokerServerForTest(t, routes, fakeBrokerMetadata{})
	response := httptest.NewRecorder()
	request := brokerRequest(t, http.MethodGet, "/routes", "")
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var result route.ListResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 2 || result.Routes[0].Hostname != "app.example.com" || result.Routes[1].Hostname != "other-project.example.com" {
		t.Fatalf("routes = %#v", result.Routes)
	}
}

func TestServerRequiresMTLSCertificate(t *testing.T) {
	server := brokerServerForTest(t, &fakeBrokerRoutes{}, fakeBrokerMetadata{})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/routes", bytes.NewBufferString(`{}`))
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
}

func brokerServerForTest(t *testing.T, routes route.Manager, metadata RouteMetadataStore) Server {
	t.Helper()
	admin := scconfig.LoadAdminFromEnv()
	admin.InfrastructureHost = "203.0.113.10"
	return Server{
		Admin:         admin,
		Projects:      projectStoreForBrokerTest(t),
		Machines:      fakeBrokerMachineStore{},
		Routes:        routes,
		RouteMetadata: metadata,
		Resolver:      fakeBrokerDNSResolver{hosts: []string{"203.0.113.10"}},
		Trust:         fakeTrustMapper{principal: Principal{Owner: "alice", Projects: []string{"sc-acme"}}},
	}
}

func brokerRequest(t *testing.T, method string, path string, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	cert := &x509.Certificate{Raw: []byte("client-cert")}
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	return request
}

func projectStoreForBrokerTest(t *testing.T) project.MemoryStore {
	t.Helper()
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "other"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	return project.MemoryStore{Projects: []project.IncusProject{{Name: "sc-acme", Config: configMap}}}
}

type recordingBrokerMetadata struct {
	route    meta.Route
	hostname string
}

func (s *recordingBrokerMetadata) FindRoute(ctx context.Context, hostname string) (meta.Route, error) {
	s.hostname = hostname
	return s.route, nil
}

func decodeError(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload["error"]
}
