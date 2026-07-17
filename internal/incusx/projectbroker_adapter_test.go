package incusx

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

// fakeExtendTrust fakes usertrust.Manager (+ the EnsureClientCertificate
// extension) for the shared-client-identity fallback in
// extendTenantCertificate.
type fakeExtendTrust struct {
	grantErr    error
	grantCalls  int
	ensureCalls int
	ensurePEM   string
	ensureFound bool
	ensureErr   error
}

func (f *fakeExtendTrust) Grant(ctx context.Context, plan usertrust.UserPlan) error {
	f.grantCalls++
	return f.grantErr
}
func (f *fakeExtendTrust) Revoke(context.Context, usertrust.UserPlan) error { return nil }
func (f *fakeExtendTrust) Delete(context.Context, usertrust.UserPlan) error { return nil }
func (f *fakeExtendTrust) ListTenantUsers(context.Context, usertrust.TenantUsersPlan) (usertrust.TenantUsersResult, error) {
	return usertrust.TenantUsersResult{}, nil
}
func (f *fakeExtendTrust) CreateToken(context.Context, usertrust.UserPlan) (usertrust.TokenResult, error) {
	return usertrust.TokenResult{}, nil
}
func (f *fakeExtendTrust) EnsureClientCertificate(ctx context.Context, pem string, plan usertrust.UserPlan) (bool, error) {
	f.ensureCalls++
	f.ensurePEM = pem
	return f.ensureFound, f.ensureErr
}

var errNameMiss = fmt.Errorf("restricted certificate %q not found; create a token first and add the client certificate: %w", "sandcastle-octocat", errRestrictedCertificateNotFound)

// The live-caught defect: a SECOND tenant on a shared client keypair has its
// projects granted at login by fingerprint (the trust entry is named after the
// FIRST tenant), so the tenant plane's name-based Grant misses. With the
// tenant's recorded certificate the grant must fall back to the fingerprint
// union instead of failing project create with a 500.
func TestExtendTenantCertificateFallsBackToFingerprintUnion(t *testing.T) {
	trust := &fakeExtendTrust{grantErr: errNameMiss, ensureFound: true}
	err := extendTenantCertificate(context.Background(), trust, usertrust.UserPlan{User: "octocat"}, "PEM")
	if err != nil {
		t.Fatalf("expected fallback success, got %v", err)
	}
	if trust.grantCalls != 1 || trust.ensureCalls != 1 || trust.ensurePEM != "PEM" {
		t.Fatalf("expected one Grant and one EnsureClientCertificate(PEM), got grants=%d ensures=%d pem=%q",
			trust.grantCalls, trust.ensureCalls, trust.ensurePEM)
	}
}

func TestExtendTenantCertificateNameMatchSkipsFallback(t *testing.T) {
	trust := &fakeExtendTrust{}
	if err := extendTenantCertificate(context.Background(), trust, usertrust.UserPlan{}, "PEM"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if trust.ensureCalls != 0 {
		t.Fatalf("EnsureClientCertificate must not run when the name lookup succeeds")
	}
}

func TestExtendTenantCertificateWithoutPEMKeepsNotFoundError(t *testing.T) {
	trust := &fakeExtendTrust{grantErr: errNameMiss}
	err := extendTenantCertificate(context.Background(), trust, usertrust.UserPlan{}, "")
	if !errors.Is(err, errRestrictedCertificateNotFound) {
		t.Fatalf("expected the not-found error to surface, got %v", err)
	}
	if trust.ensureCalls != 0 {
		t.Fatalf("no fallback without a recorded client certificate")
	}
}

func TestExtendTenantCertificateUntrustedCertificateKeepsNotFoundError(t *testing.T) {
	trust := &fakeExtendTrust{grantErr: errNameMiss, ensureFound: false}
	err := extendTenantCertificate(context.Background(), trust, usertrust.UserPlan{}, "PEM")
	if !errors.Is(err, errRestrictedCertificateNotFound) {
		t.Fatalf("expected the not-found error when the certificate is not trusted, got %v", err)
	}
}

// The Grant name miss must be a recognizable sentinel for the fallback above.
func TestGrantNotFoundErrorIsSentinel(t *testing.T) {
	server := &fakeTrustServer{}
	m := TrustManager{Server: server}
	err := m.Grant(context.Background(), usertrust.UserPlan{CertificateName: "sandcastle-ghost"})
	if !errors.Is(err, errRestrictedCertificateNotFound) {
		t.Fatalf("Grant name miss must wrap errRestrictedCertificateNotFound, got %v", err)
	}
}
