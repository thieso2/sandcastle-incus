package routebroker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

type fakeTrustMapper struct {
	principal Principal
	err       error
}

func (m fakeTrustMapper) PrincipalForFingerprint(ctx context.Context, fingerprint string) (Principal, error) {
	return m.principal, m.err
}

func TestPrincipalFromFingerprintMapsOwner(t *testing.T) {
	principal, err := PrincipalFromFingerprint(context.Background(), fakeTrustMapper{principal: Principal{
		Owner:    "alice",
		Projects: []string{" sc-alice-myproject ", "", "sc-alice-myproject"},
	}}, " abc123 ")
	if err != nil {
		t.Fatal(err)
	}
	if principal.Fingerprint != "abc123" || principal.Owner != "alice" {
		t.Fatalf("principal = %#v", principal)
	}
	if len(principal.Projects) != 1 || principal.Projects[0] != "sc-alice-myproject" {
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

func TestPrincipalFromFingerprintRejectsInvalidOwner(t *testing.T) {
	_, err := PrincipalFromFingerprint(context.Background(), fakeTrustMapper{principal: Principal{
		Owner:    "bad_owner",
		Projects: []string{"sc-bad-owner-myproject"},
	}}, "abc123")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid Sandcastle owner") {
		t.Fatalf("error = %q", err)
	}
}

func TestPrincipalFromFingerprintRejectsInvalidProjectGrant(t *testing.T) {
	_, err := PrincipalFromFingerprint(context.Background(), fakeTrustMapper{principal: Principal{
		Owner:    "alice",
		Projects: []string{"sc-alice-myproject", "bad_project"},
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

func TestAuthorizeAddAllowsOwner(t *testing.T) {
	err := AuthorizeAdd(Principal{Owner: "alice", Projects: []string{"sc-alice-myproject"}}, route.AddPlan{Project: project.Summary{Owner: "alice", IncusName: "sc-alice-myproject"}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizeAddRejectsDifferentOwner(t *testing.T) {
	err := AuthorizeAdd(Principal{Owner: "bob", Projects: []string{"sc-alice-myproject"}}, route.AddPlan{Project: project.Summary{Owner: "alice", IncusName: "sc-alice-myproject"}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAuthorizeAddRejectsProjectOutsideCertificateScope(t *testing.T) {
	err := AuthorizeAdd(Principal{Owner: "alice", Projects: []string{"sc-alice-other"}}, route.AddPlan{Project: project.Summary{Owner: "alice", IncusName: "sc-alice-myproject"}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("error = %q", err)
	}
}

func TestAuthorizeRemoveUsesStoredRouteOwner(t *testing.T) {
	err := AuthorizeRemove(Principal{Owner: "alice", Projects: []string{"sc-alice-myproject"}}, meta.Route{TargetOwner: "alice", TargetProject: "myproject"}, "sc")
	if err != nil {
		t.Fatal(err)
	}
	err = AuthorizeRemove(Principal{Owner: "bob", Projects: []string{"sc-alice-myproject"}}, meta.Route{TargetOwner: "alice", TargetProject: "myproject"}, "sc")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAuthorizeRemoveRejectsProjectOutsideCertificateScope(t *testing.T) {
	err := AuthorizeRemove(Principal{Owner: "alice", Projects: []string{"sc-alice-other"}}, meta.Route{TargetOwner: "alice", TargetProject: "myproject"}, "sc")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("error = %q", err)
	}
}
