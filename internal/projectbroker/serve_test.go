package projectbroker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeMapper struct {
	tenant   string
	projects []string
	err      error
}

func (f fakeMapper) PrincipalForFingerprint(_ context.Context, fp string) (TrustPrincipal, error) {
	if f.err != nil {
		return TrustPrincipal{}, f.err
	}
	return TrustPrincipal{Fingerprint: fp, User: f.tenant, Projects: f.projects}, nil
}

type fakeCreator struct {
	gotTenant  string
	gotProject string
	result     ProjectResult
}

func (f *fakeCreator) CreateTenantProject(_ context.Context, tenant, project string, _ string) (ProjectResult, error) {
	f.gotTenant = tenant
	f.gotProject = project
	return f.result, nil
}

func requestWithCert() *httptest.ResponseRecorder {
	return httptest.NewRecorder()
}

func TestHandlerCreatesProjectForMappedTenant(t *testing.T) {
	creator := &fakeCreator{result: ProjectResult{Tenant: "acme", Project: "backend", IncusProject: "sc2-acme-backend"}}
	h := Handler{Trust: fakeMapper{tenant: "acme", projects: []string{"sc2-acme-default"}}, Creator: creator}

	req := httptest.NewRequest("POST", "/v2/projects", strings.NewReader(`{"project":"backend"}`))
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{Raw: []byte("dummy-der")}}}
	rec := requestWithCert()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if creator.gotTenant != "acme" || creator.gotProject != "backend" {
		t.Fatalf("creator got tenant=%q project=%q", creator.gotTenant, creator.gotProject)
	}
	var got ProjectResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.IncusProject != "sc2-acme-backend" {
		t.Fatalf("IncusProject = %q", got.IncusProject)
	}
}

func TestHandlerRejectsMissingClientCert(t *testing.T) {
	h := Handler{Trust: fakeMapper{tenant: "acme"}, Creator: &fakeCreator{}}
	req := httptest.NewRequest("POST", "/v2/projects", strings.NewReader(`{"project":"backend"}`))
	rec := requestWithCert()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandlerRejectsInvalidProjectName(t *testing.T) {
	h := Handler{Trust: fakeMapper{tenant: "acme"}, Creator: &fakeCreator{}}
	req := httptest.NewRequest("POST", "/v2/projects", strings.NewReader(`{"project":"Bad Name"}`))
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{Raw: []byte("der")}}}
	rec := requestWithCert()
	h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

type fakeAdmin struct{ ok bool }

func (f fakeAdmin) IsAdmin(_ context.Context, _ string) (bool, error) { return f.ok, nil }

type fakeProvisioner struct{ got string }

func (f *fakeProvisioner) CreateTenant(_ context.Context, req TenantRequest) (TenantResult, error) {
	f.got = req.Tenant
	return TenantResult{Tenant: req.Tenant, InfraProject: "sc2-" + req.Tenant, Token: "tok"}, nil
}

func tenantReq(t *testing.T, admin bool) *httptest.ResponseRecorder {
	prov := &fakeProvisioner{}
	h := Handler{Admin: fakeAdmin{ok: admin}, Provisioner: prov}
	req := httptest.NewRequest("POST", "/v2/tenants", strings.NewReader(`{"tenant":"acme"}`))
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{Raw: []byte("der")}}}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlerAdminTenantCreateAuthorized(t *testing.T) {
	rec := tenantReq(t, true)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerAdminTenantCreateRejectsNonAdmin(t *testing.T) {
	rec := tenantReq(t, false)
	if rec.Code != 403 {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

type fakeSidecarUpdater struct {
	gotTenant string
	result    SidecarUpdateResult
	err       error
}

func (f *fakeSidecarUpdater) UpdateTenantSidecar(tenant string) (SidecarUpdateResult, error) {
	f.gotTenant = tenant
	if f.err != nil {
		return SidecarUpdateResult{}, f.err
	}
	return f.result, nil
}

func TestHandlerSidecarUpdateForMappedTenant(t *testing.T) {
	updater := &fakeSidecarUpdater{result: SidecarUpdateResult{Tenant: "acme", Project: "sc2-acme", Instance: "sidecar", BinaryVersion: "v0.2.0"}}
	h := Handler{Trust: fakeMapper{tenant: "acme"}, Sidecars: updater}

	req := httptest.NewRequest("POST", "/v2/sidecar/update", nil)
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{Raw: []byte("dummy-der")}}}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if updater.gotTenant != "acme" {
		t.Fatalf("updater got tenant %q", updater.gotTenant)
	}
	var got SidecarUpdateResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.BinaryVersion != "v0.2.0" || got.Instance != "sidecar" {
		t.Fatalf("result = %+v", got)
	}
}

func TestHandlerSidecarUpdateNotConfigured(t *testing.T) {
	h := Handler{Trust: fakeMapper{tenant: "acme"}}
	req := httptest.NewRequest("POST", "/v2/sidecar/update", nil)
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{Raw: []byte("dummy-der")}}}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 501 {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}
