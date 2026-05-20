package routebroker

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

type fakeBrokerRoutes struct {
	added   *route.AddPlan
	removed *route.RemovePlan
	list    route.ListResult
}

func (r *fakeBrokerRoutes) Add(ctx context.Context, plan route.AddPlan) error {
	r.added = &plan
	return nil
}

func (r *fakeBrokerRoutes) Remove(ctx context.Context, plan route.RemovePlan) error {
	r.removed = &plan
	return nil
}

func (r *fakeBrokerRoutes) List(ctx context.Context, plan route.ListPlan) (route.ListResult, error) {
	return r.list, nil
}

type fakeBrokerSandboxStore struct{}

func (s fakeBrokerSandboxStore) FindSandbox(ctx context.Context, summary project.Summary, name string) (meta.Sandbox, error) {
	return meta.Sandbox{
		Owner:     summary.Owner,
		Project:   summary.Name,
		Name:      name,
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

func TestServerAddsAuthorizedRoute(t *testing.T) {
	routes := &fakeBrokerRoutes{}
	server := brokerServerForTest(t, routes, fakeBrokerMetadata{})
	response := httptest.NewRecorder()
	request := brokerRequest(t, http.MethodPost, "/routes", `{"hostname":"app.example.com","targetReference":"alice/myproject/codex"}`)
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
}

func TestServerRejectsUnownedRouteAdd(t *testing.T) {
	routes := &fakeBrokerRoutes{}
	server := brokerServerForTest(t, routes, fakeBrokerMetadata{})
	server.Trust = fakeTrustMapper{owner: "bob"}
	response := httptest.NewRecorder()
	request := brokerRequest(t, http.MethodPost, "/routes", `{"hostname":"app.example.com","targetReference":"alice/myproject/codex"}`)
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if routes.added != nil {
		t.Fatal("route should not be added")
	}
}

func TestServerRemovesAuthorizedRoute(t *testing.T) {
	routes := &fakeBrokerRoutes{}
	server := brokerServerForTest(t, routes, fakeBrokerMetadata{route: meta.Route{
		Hostname:      "app.example.com",
		TargetOwner:   "alice",
		TargetProject: "myproject",
		TargetSandbox: "codex",
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

func TestServerListsOnlyPrincipalRoutes(t *testing.T) {
	routes := &fakeBrokerRoutes{list: route.ListResult{Routes: []route.Route{
		{Hostname: "app.example.com", TargetReference: "alice/myproject/codex", RoutePort: 3000},
		{Hostname: "other.example.com", TargetReference: "bob/myproject/codex", RoutePort: 3000},
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
	if len(result.Routes) != 1 || result.Routes[0].Hostname != "app.example.com" {
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
	return Server{
		Admin:         scconfig.LoadAdminFromEnv(),
		Projects:      projectStoreForBrokerTest(t),
		Sandboxes:     fakeBrokerSandboxStore{},
		Routes:        routes,
		RouteMetadata: metadata,
		Trust:         fakeTrustMapper{owner: "alice"},
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
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	return project.MemoryStore{Projects: []project.IncusProject{{Name: "sc-alice-myproject", Config: configMap}}}
}

func decodeError(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload["error"]
}
