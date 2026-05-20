package incusx

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
)

type fakeRouteBrokerTrustServer struct {
	certificates []api.Certificate
	err          error
}

func (s fakeRouteBrokerTrustServer) GetCertificates() ([]api.Certificate, error) {
	return s.certificates, s.err
}

func TestRouteBrokerTrustMapperMapsSandcastleCertificateName(t *testing.T) {
	mapper := RouteBrokerTrustMapper{Server: fakeRouteBrokerTrustServer{certificates: []api.Certificate{{
		CertificatePut: api.CertificatePut{
			Name:       "sandcastle-alice",
			Type:       api.CertificateTypeClient,
			Restricted: true,
			Projects:   []string{"sc-alice-myproject"},
		},
		Fingerprint: "AB:CD",
	}}}}
	principal, err := mapper.PrincipalForFingerprint(context.Background(), "abcd")
	if err != nil {
		t.Fatal(err)
	}
	if principal.Owner != "alice" {
		t.Fatalf("owner = %q", principal.Owner)
	}
	if len(principal.Projects) != 1 || principal.Projects[0] != "sc-alice-myproject" {
		t.Fatalf("projects = %#v", principal.Projects)
	}
}

func TestRouteBrokerTrustMapperRejectsNonSandcastleCertificate(t *testing.T) {
	mapper := RouteBrokerTrustMapper{Server: fakeRouteBrokerTrustServer{certificates: []api.Certificate{{
		CertificatePut: api.CertificatePut{
			Name:       "admin",
			Type:       api.CertificateTypeClient,
			Restricted: true,
		},
		Fingerprint: "abcd",
	}}}}
	_, err := mapper.PrincipalForFingerprint(context.Background(), "abcd")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not a Sandcastle") {
		t.Fatalf("error = %q", err)
	}
}

func TestRouteBrokerTrustMapperRejectsUnrestrictedCertificate(t *testing.T) {
	mapper := RouteBrokerTrustMapper{Server: fakeRouteBrokerTrustServer{certificates: []api.Certificate{{
		CertificatePut: api.CertificatePut{
			Name:       "sandcastle-alice",
			Type:       api.CertificateTypeClient,
			Restricted: false,
		},
		Fingerprint: "abcd",
	}}}}
	_, err := mapper.PrincipalForFingerprint(context.Background(), "abcd")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not a Sandcastle") {
		t.Fatalf("error = %q", err)
	}
}

func TestRouteBrokerTrustMapperRejectsNonClientCertificate(t *testing.T) {
	mapper := RouteBrokerTrustMapper{Server: fakeRouteBrokerTrustServer{certificates: []api.Certificate{{
		CertificatePut: api.CertificatePut{
			Name:       "sandcastle-alice",
			Type:       api.CertificateTypeServer,
			Restricted: true,
		},
		Fingerprint: "abcd",
	}}}}
	_, err := mapper.PrincipalForFingerprint(context.Background(), "abcd")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not a Sandcastle") {
		t.Fatalf("error = %q", err)
	}
}

func TestRouteBrokerTrustMapperRejectsUnknownFingerprint(t *testing.T) {
	mapper := RouteBrokerTrustMapper{Server: fakeRouteBrokerTrustServer{}}
	_, err := mapper.PrincipalForFingerprint(context.Background(), "abcd")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRouteBrokerTrustMapperWrapsListErrors(t *testing.T) {
	mapper := RouteBrokerTrustMapper{Server: fakeRouteBrokerTrustServer{err: errors.New("boom")}}
	_, err := mapper.PrincipalForFingerprint(context.Background(), "abcd")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "list Incus certificates") {
		t.Fatalf("error = %q", err)
	}
}
