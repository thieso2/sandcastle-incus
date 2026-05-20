package incusx

import (
	"context"
	"testing"

	"github.com/gorilla/websocket"
	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

type fakeTrustServer struct {
	certificates []api.Certificate
	updated      *api.CertificatePut
	tokenOp      incus.Operation
}

func (s *fakeTrustServer) GetCertificates() ([]api.Certificate, error) { return s.certificates, nil }
func (s *fakeTrustServer) UpdateCertificate(fingerprint string, certificate api.CertificatePut, etag string) error {
	s.updated = &certificate
	return nil
}
func (s *fakeTrustServer) CreateCertificateToken(certificate api.CertificatesPost) (incus.Operation, error) {
	return s.tokenOp, nil
}

func TestTrustManagerGrant(t *testing.T) {
	server := &fakeTrustServer{certificates: []api.Certificate{{
		Fingerprint: "abc",
		CertificatePut: api.CertificatePut{
			Name:       "sandcastle-alice",
			Type:       api.CertificateTypeClient,
			Restricted: true,
			Projects:   []string{"sc-alice-existing"},
		},
	}}}
	manager := TrustManager{Server: server}
	err := manager.Grant(context.Background(), usertrust.UserPlan{
		User:            "alice",
		CertificateName: "sandcastle-alice",
		Restricted:      true,
		Projects:        []string{"sc-alice-existing", "sc-alice-new"},
		Description:     "Sandcastle restricted user alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.updated == nil {
		t.Fatal("expected certificate update")
	}
	if !server.updated.Restricted {
		t.Fatal("Restricted = false, want true")
	}
	if len(server.updated.Projects) != 2 {
		t.Fatalf("Projects = %#v", server.updated.Projects)
	}
}

func TestTrustManagerGrantRejectsUnrestrictedCertificate(t *testing.T) {
	server := &fakeTrustServer{certificates: []api.Certificate{{
		Fingerprint: "abc",
		CertificatePut: api.CertificatePut{
			Name:       "sandcastle-alice",
			Type:       api.CertificateTypeClient,
			Restricted: false,
		},
	}}}
	manager := TrustManager{Server: server}
	err := manager.Grant(context.Background(), usertrust.UserPlan{
		User:            "alice",
		CertificateName: "sandcastle-alice",
		Restricted:      true,
		Projects:        []string{"sc-alice-new"},
		Description:     "Sandcastle restricted user alice",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if server.updated != nil {
		t.Fatal("certificate should not be updated")
	}
}

func TestTrustManagerGrantRejectsServerCertificate(t *testing.T) {
	server := &fakeTrustServer{certificates: []api.Certificate{{
		Fingerprint: "abc",
		CertificatePut: api.CertificatePut{
			Name:       "sandcastle-alice",
			Type:       api.CertificateTypeServer,
			Restricted: true,
		},
	}}}
	manager := TrustManager{Server: server}
	err := manager.Grant(context.Background(), usertrust.UserPlan{
		User:            "alice",
		CertificateName: "sandcastle-alice",
		Restricted:      true,
		Projects:        []string{"sc-alice-new"},
		Description:     "Sandcastle restricted user alice",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if server.updated != nil {
		t.Fatal("certificate should not be updated")
	}
}

func TestTrustManagerCreateToken(t *testing.T) {
	server := &fakeTrustServer{tokenOp: tokenOperation()}
	manager := TrustManager{Server: server}
	result, err := manager.CreateToken(context.Background(), usertrust.UserPlan{
		User:            "alice",
		CertificateName: "sandcastle-alice",
		Restricted:      true,
		Projects:        []string{"sc-alice-myproject"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Token == "" {
		t.Fatal("expected token")
	}
}

func tokenOperation() incus.Operation {
	return staticOperation{op: api.Operation{Metadata: map[string]any{
		"request":     map[string]any{"name": "sandcastle-alice"},
		"secret":      "secret",
		"fingerprint": "fingerprint",
		"addresses":   []any{"127.0.0.1:8443"},
	}}}
}

type staticOperation struct {
	op api.Operation
}

func (o staticOperation) AddHandler(func(api.Operation)) (*incus.EventTarget, error) { return nil, nil }
func (o staticOperation) Cancel() error                                              { return nil }
func (o staticOperation) Get() api.Operation                                         { return o.op }
func (o staticOperation) GetWebsocket(string) (*websocket.Conn, error)               { return nil, nil }
func (o staticOperation) RemoveHandler(*incus.EventTarget) error                     { return nil }
func (o staticOperation) Refresh() error                                             { return nil }
func (o staticOperation) Wait() error                                                { return nil }
func (o staticOperation) WaitContext(context.Context) error                          { return nil }
