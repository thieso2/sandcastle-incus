package authapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

// resourceCacheAPITestRenderer is a minimal stand-in for
// incusx.MachineFromInstance: the real conversion lives in incusx, which
// imports authapp, so a test in this package cannot call it without an
// import cycle. It carries just enough (name/project/tenant/type) to assert
// the endpoint's filtering and tenant-scoping, which is what this file tests
// — not NIC-address resolution, which incusx's own tests already cover.
func resourceCacheAPITestRenderer(tenantName string, project string, instance api.InstanceFull) (meta.Machine, bool) {
	if meta.IsManaged(instance.Config) && instance.Config[meta.KeyKind] == meta.KindSidecar {
		return meta.Machine{}, false
	}
	return meta.Machine{Tenant: tenantName, Project: project, Name: instance.Name, Type: string(instance.Type)}, true
}

// resourceAPITestHandler builds a handler with two v2 tenants — "alice" (the
// caller, with a "docker" app project beyond the default one) and "bob" (a
// different tenant, used to assert the cache-backed endpoint never leaks
// across tenants) — and returns it plus alice's bearer token.
func resourceAPITestHandler(t *testing.T, cache *ResourceCache) (http.Handler, string) {
	t.Helper()
	db := authDBForTest(t)
	if err := UpsertUser(context.Background(), db, User{UserKey: "alice", GitHubUsername: "alice", Allowlisted: true}); err != nil {
		t.Fatal(err)
	}
	token, err := CreateCLIToken(context.Background(), db, "alice", timeNow())
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(db, HandlerOptions{
		AuthHostname: "sc2.thieso2.dev",
		Admin:        testAuthAdminConfig(),
		Tenants: tenant.MemoryStore{Projects: v2TenantProjectsForAuthTest(
			authTestTenant{Tenant: "alice", UnixUser: "alice", CIDR: "10.248.1.0/24", Projects: []string{"docker"}},
			authTestTenant{Tenant: "bob", UnixUser: "bob", CIDR: "10.248.2.0/24"},
		)},
		TenantAccess:                 &fakeTenantAccessManager{},
		ResourceCache:                cache,
		ResourceCacheMachineRenderer: resourceCacheAPITestRenderer,
	})
	return h, token
}

func readyResourceCacheForTest() *ResourceCache {
	cache := NewResourceCache(time.Minute)
	cache.seed(
		[]api.InstanceFull{
			{Instance: api.Instance{Project: "sc2-alice-default", Name: "web", Type: "container"}},
			{Instance: api.Instance{Project: "sc2-alice-docker", Name: "db", Type: "container"}},
			{Instance: api.Instance{Project: "sc2-bob-default", Name: "secret", Type: "container"}},
		},
		nil, nil, nil, nil, nil,
	)
	cache.markStreamConnected()
	return cache
}

func TestResourcesAPI_ToggleOffAnswers503(t *testing.T) {
	h, token := resourceAPITestHandler(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/resources?tenant=alice", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("toggle off = %d %q, want 503", res.Code, res.Body.String())
	}
}

func TestResourcesAPI_NotReadyAnswers503(t *testing.T) {
	// Seeded is not enough — the event stream must also be connected (t1's
	// single readiness gate). No seed(), no markStreamConnected() here at all:
	// this is "auth-app just started" or "the event stream is down".
	cache := NewResourceCache(time.Minute)
	h, token := resourceAPITestHandler(t, cache)
	req := httptest.NewRequest(http.MethodGet, "/api/resources?tenant=alice", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("not ready = %d %q, want 503", res.Code, res.Body.String())
	}
}

func TestResourcesAPI_RequiresBearerToken(t *testing.T) {
	h, _ := resourceAPITestHandler(t, readyResourceCacheForTest())
	req := httptest.NewRequest(http.MethodGet, "/api/resources?tenant=alice", nil)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d %q, want 401", res.Code, res.Body.String())
	}
}

func TestResourcesAPI_ReadyReturnsFilteredResultsScopedToCaller(t *testing.T) {
	h, token := resourceAPITestHandler(t, readyResourceCacheForTest())

	req := httptest.NewRequest(http.MethodGet, "/api/resources?tenant=alice", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("ready = %d %q, want 200", res.Code, res.Body.String())
	}
	var result ResourceListResult
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Machines) != 2 {
		t.Fatalf("machines = %+v, want 2 (alice's own across both her projects, never bob's)", result.Machines)
	}
	for _, m := range result.Machines {
		if m.Name == "secret" {
			t.Fatalf("leaked bob's instance into alice's listing: %+v", result.Machines)
		}
	}
	if !result.AllProjects {
		t.Fatalf("AllProjects = false with no project filter")
	}
}

func TestResourcesAPI_FiltersByProjectAndMachine(t *testing.T) {
	h, token := resourceAPITestHandler(t, readyResourceCacheForTest())

	req := httptest.NewRequest(http.MethodGet, "/api/resources?tenant=alice&project=docker", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("project filter = %d %q", res.Code, res.Body.String())
	}
	var result ResourceListResult
	json.Unmarshal(res.Body.Bytes(), &result)
	if len(result.Machines) != 1 || result.Machines[0].Name != "db" {
		t.Fatalf("project=docker machines = %+v, want just db", result.Machines)
	}
	if result.AllProjects {
		t.Fatalf("AllProjects = true with an explicit project filter")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/resources?tenant=alice&machine=web", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("machine filter = %d %q", res.Code, res.Body.String())
	}
	result = ResourceListResult{}
	json.Unmarshal(res.Body.Bytes(), &result)
	if len(result.Machines) != 1 || result.Machines[0].Name != "web" {
		t.Fatalf("machine=web machines = %+v, want just web", result.Machines)
	}
}

func TestResourcesAPI_UnknownLiteralProjectIs404(t *testing.T) {
	h, token := resourceAPITestHandler(t, readyResourceCacheForTest())
	req := httptest.NewRequest(http.MethodGet, "/api/resources?tenant=alice&project=nosuchproject", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("unknown project = %d %q, want 404", res.Code, res.Body.String())
	}
}

func TestResourcesAPI_RejectsCrossTenantRequest(t *testing.T) {
	h, token := resourceAPITestHandler(t, readyResourceCacheForTest())
	req := httptest.NewRequest(http.MethodGet, "/api/resources?tenant=bob", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant request = %d %q, want 403", res.Code, res.Body.String())
	}
}
