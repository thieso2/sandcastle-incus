package routebroker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

type fakeTrustMapper struct {
	owner string
	err   error
}

func (m fakeTrustMapper) OwnerForFingerprint(ctx context.Context, fingerprint string) (string, error) {
	return m.owner, m.err
}

func TestPrincipalFromFingerprintMapsOwner(t *testing.T) {
	principal, err := PrincipalFromFingerprint(context.Background(), fakeTrustMapper{owner: "alice"}, " abc123 ")
	if err != nil {
		t.Fatal(err)
	}
	if principal.Fingerprint != "abc123" || principal.Owner != "alice" {
		t.Fatalf("principal = %#v", principal)
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

func TestPrincipalFromFingerprintReturnsMapperError(t *testing.T) {
	_, err := PrincipalFromFingerprint(context.Background(), fakeTrustMapper{err: errors.New("boom")}, "abc123")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAuthorizeAddAllowsOwner(t *testing.T) {
	err := AuthorizeAdd(Principal{Owner: "alice"}, route.AddPlan{Project: project.Summary{Owner: "alice"}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizeAddRejectsDifferentOwner(t *testing.T) {
	err := AuthorizeAdd(Principal{Owner: "bob"}, route.AddPlan{Project: project.Summary{Owner: "alice"}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAuthorizeRemoveUsesStoredRouteOwner(t *testing.T) {
	err := AuthorizeRemove(Principal{Owner: "alice"}, meta.Route{TargetOwner: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	err = AuthorizeRemove(Principal{Owner: "bob"}, meta.Route{TargetOwner: "alice"})
	if err == nil {
		t.Fatal("expected error")
	}
}
