package authapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestEnableMachineWorkloadIdentityRotatesSecretAndStoresVerifierOnly(t *testing.T) {
	db := authDBForTest(t)
	first, err := EnableMachineWorkloadIdentity(context.Background(), db, "auth.example.com", MachineRuntimeSecretRequest{
		Tenant:         "acme",
		Project:        "default",
		Machine:        "codex",
		UserKey:        "octocat",
		GitHubUsername: "OctoCat",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnableMachineWorkloadIdentity(context.Background(), db, "auth.example.com", MachineRuntimeSecretRequest{
		Tenant:         "acme",
		Project:        "default",
		Machine:        "codex",
		UserKey:        "octocat",
		GitHubUsername: "OctoCat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.RuntimeSecret == "" || second.RuntimeSecret == "" || first.RuntimeSecret == second.RuntimeSecret {
		t.Fatalf("secrets were not rotated: %#v %#v", first, second)
	}
	if first.TokenEndpoint != "https://auth.example.com/internal/workload/token" {
		t.Fatalf("token endpoint = %q", first.TokenEndpoint)
	}
	var stored string
	if err := db.QueryRow("SELECT secret_verifier FROM machine_runtime_secrets WHERE tenant = 'acme' AND project = 'default' AND machine = 'codex'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, first.RuntimeSecret) || strings.Contains(stored, second.RuntimeSecret) {
		t.Fatalf("stored verifier leaked runtime secret: %q", stored)
	}
}

func TestWorkloadTokenEndpointMintsClaimsAndRejectsBadSecret(t *testing.T) {
	db := authDBForTest(t)
	enabled, err := EnableMachineWorkloadIdentity(context.Background(), db, "auth.example.com", MachineRuntimeSecretRequest{
		Tenant:         "acme",
		Project:        "default",
		Machine:        "codex",
		UserKey:        "octocat",
		GitHubUsername: "OctoCat",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db, HandlerOptions{
		AuthHostname: "auth.example.com",
		Tenants: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: tenantConfigForAuthTest(t, meta.Tenant{Tenant: "acme", PrivateCIDR: "10.248.0.0/24"}),
		}}},
		Machines: fakeWorkloadMachineStore{machines: []meta.Machine{{Tenant: "acme", Project: "default", Name: "codex"}}},
	})
	requestBody := `{"tenant":"acme","project":"default","machine":"codex","runtime_secret":"` + enabled.RuntimeSecret + `"}`
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/internal/workload/token", strings.NewReader(requestBody))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("token = %d %q", response.Code, response.Body.String())
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ExpiresIn != int((15 * time.Minute).Seconds()) {
		t.Fatalf("expires_in = %d", payload.ExpiresIn)
	}
	claims := jwtClaimsForTest(t, payload.AccessToken)
	for key, want := range map[string]string{
		"iss":                 "https://auth.example.com",
		"tenant":              "acme",
		"project":             "default",
		"machine":             "codex",
		"sandcastle_user_key": "octocat",
		"github_username":     "OctoCat",
	} {
		if claims[key] != want {
			t.Fatalf("claim %s = %#v, want %q in %#v", key, claims[key], want, claims)
		}
	}
	if _, ok := claims["sandbox"]; ok {
		t.Fatalf("claims contain legacy sandbox vocabulary: %#v", claims)
	}

	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/internal/workload/token", strings.NewReader(`{"tenant":"acme","project":"default","machine":"codex","runtime_secret":"bad"}`)))
	if bad.Code != http.StatusForbidden {
		t.Fatalf("bad secret = %d %q", bad.Code, bad.Body.String())
	}
}

func TestWorkloadTokenEndpointRejectsDisabledAndDeletedMachine(t *testing.T) {
	db := authDBForTest(t)
	enabled, err := EnableMachineWorkloadIdentity(context.Background(), db, "auth.example.com", MachineRuntimeSecretRequest{
		Tenant:  "acme",
		Project: "default",
		Machine: "codex",
		UserKey: "octocat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE machine_runtime_secrets SET enabled = 0 WHERE tenant = 'acme'"); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db, HandlerOptions{AuthHostname: "auth.example.com", Tenants: tenant.MemoryStore{}, Machines: fakeWorkloadMachineStore{}})
	body := `{"tenant":"acme","project":"default","machine":"codex","runtime_secret":"` + enabled.RuntimeSecret + `"}`
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/internal/workload/token", strings.NewReader(body)))
	if response.Code != http.StatusForbidden {
		t.Fatalf("disabled = %d %q", response.Code, response.Body.String())
	}

	if _, err := db.Exec("UPDATE machine_runtime_secrets SET enabled = 1 WHERE tenant = 'acme'"); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/internal/workload/token", strings.NewReader(body)))
	if response.Code != http.StatusForbidden {
		t.Fatalf("deleted = %d %q", response.Code, response.Body.String())
	}
}

type fakeWorkloadMachineStore struct {
	machines []meta.Machine
}

func (s fakeWorkloadMachineStore) ListMachines(ctx context.Context, summary tenant.Summary) ([]meta.Machine, error) {
	return append([]meta.Machine{}, s.machines...), nil
}

func jwtClaimsForTest(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT parts = %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}
