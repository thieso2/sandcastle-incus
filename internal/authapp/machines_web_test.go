package authapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestMachinesWebRedirectsWhenNotLoggedIn(t *testing.T) {
	db := authDBForTest(t)
	request := httptest.NewRequest(http.MethodGet, "/machines", nil)
	response := httptest.NewRecorder()

	NewHandler(db, HandlerOptions{}).ServeHTTP(response, request)
	if response.Code != http.StatusFound || response.Header().Get("Location") != "/" {
		t.Fatalf("machines redirect = %d %q", response.Code, response.Header().Get("Location"))
	}
}

func TestMachinesWebListsAccessibleMachinesWithSSHLink(t *testing.T) {
	db := authDBForTest(t)
	if err := UpsertUser(context.Background(), db, User{UserKey: "alice", GitHubUsername: "alice", Allowlisted: true}); err != nil {
		t.Fatal(err)
	}
	sessionID, err := CreateSession(context.Background(), db, "alice", time.Now())
	if err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(db, HandlerOptions{
		Admin: testAuthAdminConfig(),
		Tenants: tenant.MemoryStore{Projects: []tenant.IncusProject{
			{Name: "sc-acme", Config: tenantConfigForAuthTest(t, meta.Tenant{Tenant: "acme", UnixUser: "alice", PrivateCIDR: "10.248.1.0/24"})},
			{Name: "sc-other", Config: tenantConfigForAuthTest(t, meta.Tenant{Tenant: "other", PrivateCIDR: "10.248.2.0/24"})},
		}},
		TenantAccess: &fakeTenantAccessManager{usersByTenant: map[string][]string{"acme": {"alice"}}},
		Machines: fakeMachinesByTenant{machines: map[string][]meta.Machine{
			"acme":  {{Tenant: "acme", Project: "default", Name: "dev", PrivateIP: "10.248.1.20", Running: true}},
			"other": {{Tenant: "other", Project: "default", Name: "secret", PrivateIP: "10.248.2.9"}},
		}},
	})

	request := httptest.NewRequest(http.MethodGet, "/machines", nil)
	request.AddCookie(&http.Cookie{Name: "sandcastle_session", Value: sessionID})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("machines = %d %q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "ssh://alice@10.248.1.20") {
		t.Fatalf("expected ssh link for accessible machine, got:\n%s", body)
	}
	if !strings.Contains(body, "dev.default.acme") {
		t.Fatalf("expected FQDN for accessible machine, got:\n%s", body)
	}
	if strings.Contains(body, "10.248.2.9") || strings.Contains(body, "secret") {
		t.Fatalf("leaked machine from inaccessible tenant:\n%s", body)
	}
}

type fakeMachinesByTenant struct {
	machines map[string][]meta.Machine
}

func (s fakeMachinesByTenant) ListMachines(ctx context.Context, summary tenant.Summary) ([]meta.Machine, error) {
	return append([]meta.Machine{}, s.machines[summary.Tenant]...), nil
}
