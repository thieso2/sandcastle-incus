package routebroker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/route"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type fakeTrustMapper struct {
	principal Principal
	err       error
}

func (m fakeTrustMapper) PrincipalForFingerprint(ctx context.Context, fingerprint string) (Principal, error) {
	return m.principal, m.err
}

func TestPrincipalFromFingerprintMapsUser(t *testing.T) {
	principal, err := PrincipalFromFingerprint(context.Background(), fakeTrustMapper{principal: Principal{
		User:     "alice",
		Projects: []string{" sc-acme ", "", "sc-acme"},
	}}, " abc123 ")
	if err != nil {
		t.Fatal(err)
	}
	if principal.Fingerprint != "abc123" || principal.User != "alice" {
		t.Fatalf("principal = %#v", principal)
	}
	if len(principal.Projects) != 1 || principal.Projects[0] != "sc-acme" {
		t.Fatalf("projects = %#v", principal.Projects)
	}
}

func TestPrincipalFromFingerprintWrapsUnmappedCertificate(t *testing.T) {
	_, err := PrincipalFromFingerprint(context.Background(), fakeTrustMapper{}, "abc123")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not mapped") {
		t.Fatalf("error = %q", err)
	}
}

func TestPrincipalFromFingerprintRejectsInvalidUser(t *testing.T) {
	_, err := PrincipalFromFingerprint(context.Background(), fakeTrustMapper{principal: Principal{
		User:     "bad_user",
		Projects: []string{"sc-bad-user"},
	}}, "abc123")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid Sandcastle user") {
		t.Fatalf("error = %q", err)
	}
}

func TestPrincipalFromFingerprintRejectsInvalidProjectGrant(t *testing.T) {
	_, err := PrincipalFromFingerprint(context.Background(), fakeTrustMapper{principal: Principal{
		User:     "alice",
		Projects: []string{"sc-acme", "bad_project"},
	}}, "abc123")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid restricted project grant") {
		t.Fatalf("error = %q", err)
	}
}

func TestPrincipalFromFingerprintReturnsMapperError(t *testing.T) {
	_, err := PrincipalFromFingerprint(context.Background(), fakeTrustMapper{err: errors.New("boom")}, "abc123")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAuthorizeCreateAllowsGrantedUser(t *testing.T) {
	err := AuthorizeCreate(Principal{User: "alice", Projects: []string{"sc-acme"}}, route.CreatePlan{Tenant: tenant.Summary{Tenant: "acme", IncusName: "sc-acme"}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizeCreateIgnoresPrincipalUserForTenantAccess(t *testing.T) {
	err := AuthorizeCreate(Principal{User: "bob", Projects: []string{"sc-acme"}}, route.CreatePlan{Tenant: tenant.Summary{Tenant: "acme", IncusName: "sc-acme"}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizeCreateRejectsTenantOutsideCertificateScope(t *testing.T) {
	err := AuthorizeCreate(Principal{User: "alice", Projects: []string{"sc-other"}}, route.CreatePlan{Tenant: tenant.Summary{Tenant: "acme", IncusName: "sc-acme"}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("error = %q", err)
	}
}

func TestAuthorizeDeleteUsesStoredRouteTenant(t *testing.T) {
	err := AuthorizeDelete(Principal{User: "alice", Projects: []string{"sc-acme"}}, meta.Route{TargetTenant: "acme", TargetProject: "default"}, "sc")
	if err != nil {
		t.Fatal(err)
	}
	err = AuthorizeDelete(Principal{User: "bob", Projects: []string{"sc-acme"}}, meta.Route{TargetTenant: "acme", TargetProject: "default"}, "sc")
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizeDeleteRejectsTenantOutsideCertificateScope(t *testing.T) {
	err := AuthorizeDelete(Principal{User: "alice", Projects: []string{"sc-other"}}, meta.Route{TargetTenant: "acme", TargetProject: "default"}, "sc")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("error = %q", err)
	}
}
