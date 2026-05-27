package authapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOIDCSigningKeyBootstrapStoresEncryptedPrivateKey(t *testing.T) {
	db := authDBForTest(t)
	key, err := EnsureOIDCSigningKey(context.Background(), db, "")
	if err != nil {
		t.Fatal(err)
	}
	if key.KID == "" || key.Alg != oidcSigningAlg || key.PublicJWK == "" {
		t.Fatalf("key = %#v", key)
	}
	if strings.Contains(key.EncryptedPrivateKey, "PRIVATE KEY") || strings.Contains(key.EncryptedPrivateKey, `"d"`) {
		t.Fatalf("private key appears unencrypted: %s", key.EncryptedPrivateKey)
	}
	again, err := EnsureOIDCSigningKey(context.Background(), db, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.KID != key.KID {
		t.Fatalf("rotated unexpectedly: %q != %q", again.KID, key.KID)
	}
	var columns int
	if err := db.QueryRow("SELECT count(*) FROM pragma_table_info('oidc_signing_keys') WHERE name IN ('kid', 'alg', 'encrypted_private_key', 'public_jwk', 'active', 'not_before', 'retired_at')").Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 7 {
		t.Fatalf("rotation-ready columns = %d", columns)
	}
}

func TestOIDCSigningKeysAreScopedByTenant(t *testing.T) {
	db := authDBForTest(t)
	global, err := EnsureOIDCSigningKey(context.Background(), db, "")
	if err != nil {
		t.Fatal(err)
	}
	acme, err := EnsureOIDCSigningKey(context.Background(), db, "acme")
	if err != nil {
		t.Fatal(err)
	}
	other, err := EnsureOIDCSigningKey(context.Background(), db, "other")
	if err != nil {
		t.Fatal(err)
	}
	if global.KID == acme.KID || acme.KID == other.KID {
		t.Fatalf("tenant keys were not isolated: global=%q acme=%q other=%q", global.KID, acme.KID, other.KID)
	}
	acmeKeys, err := ListPublicOIDCSigningKeys(context.Background(), db, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(acmeKeys) != 1 || !strings.Contains(string(acmeKeys[0]), acme.KID) {
		t.Fatalf("acme keys = %#v, want kid %q", acmeKeys, acme.KID)
	}
}

func TestOIDCDiscoveryUsesConfiguredIssuerAndCacheHeaders(t *testing.T) {
	db := authDBForTest(t)
	handler := NewHandler(db, HandlerOptions{AuthHostname: "auth.example.com"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/.well-known/openid-configuration", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("discovery = %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != oidcCacheControl {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["issuer"] != "https://auth.example.com" || payload["jwks_uri"] != "https://auth.example.com/.well-known/jwks.json" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestTenantOIDCDiscoveryUsesTenantIssuerAndJWKS(t *testing.T) {
	db := authDBForTest(t)
	handler := NewHandler(db, HandlerOptions{AuthHostname: "auth.example.com"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/t/acme/.well-known/openid-configuration", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("tenant discovery = %d %q", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["issuer"] != "https://auth.example.com/t/acme" || payload["jwks_uri"] != "https://auth.example.com/t/acme/.well-known/jwks.json" {
		t.Fatalf("payload = %#v", payload)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/t/acme/.well-known/jwks.json", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("tenant jwks = %d %q", response.Code, response.Body.String())
	}
	var jwks struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &jwks); err != nil {
		t.Fatal(err)
	}
	if len(jwks.Keys) != 1 || jwks.Keys[0]["kid"] == "" {
		t.Fatalf("jwks = %#v", jwks)
	}
}

func TestOIDCJWKSExposesPublicKeysOnly(t *testing.T) {
	db := authDBForTest(t)
	handler := NewHandler(db, HandlerOptions{AuthHostname: "https://auth.example.com/"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("jwks = %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != oidcCacheControl {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	var payload struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Keys) != 1 {
		t.Fatalf("keys = %#v", payload.Keys)
	}
	key := payload.Keys[0]
	if key["kty"] != "RSA" || key["kid"] == "" || key["n"] == "" || key["e"] == "" {
		t.Fatalf("public jwk = %#v", key)
	}
	for _, forbidden := range []string{"d", "p", "q", "dp", "dq", "qi", "k"} {
		if _, ok := key[forbidden]; ok {
			t.Fatalf("JWKS exposed private member %q: %#v", forbidden, key)
		}
	}
}

func TestOIDCDiscoveryRequiresConfiguredAuthHostname(t *testing.T) {
	db := authDBForTest(t)
	response := httptest.NewRecorder()
	NewHandler(db, HandlerOptions{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/.well-known/openid-configuration", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("discovery = %d %q", response.Code, response.Body.String())
	}
}
