// Package projectbroker is the Sandcastle Broker's project-lifecycle service
// (ADR-0016). A tenant runs `sc project create <name>`, authenticated by their
// restricted Incus client certificate; the broker (holding admin credentials)
// maps the cert to the tenant, creates the app Incus project + profile, and
// extends the tenant's restricted cert to include it. It reuses the Route
// Broker's client-cert principal pattern.
package projectbroker

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/routebroker"
)

// Principal identifies the caller by their restricted Incus certificate.
type Principal struct {
	Fingerprint string
	Tenant      string
	Projects    []string
}

// TrustMapper resolves a client-certificate fingerprint to its Sandcastle
// principal (tenant + granted projects). Satisfied by incusx.RouteBrokerTrustMapper.
type TrustMapper interface {
	PrincipalForFingerprint(context.Context, string) (routebroker.Principal, error)
}

// ProjectCreator performs the privileged scaffolding: create the app Incus
// project + profile and extend the tenant's restricted cert to include it.
type ProjectCreator interface {
	CreateTenantProject(ctx context.Context, tenant string, project string) (ProjectResult, error)
}

// ProjectResult is returned to the caller after a successful create.
type ProjectResult struct {
	Tenant       string `json:"tenant"`
	Project      string `json:"project"`
	IncusProject string `json:"incusProject"`
	Bridge       string `json:"bridge"`
	DNSSuffix    string `json:"dnsSuffix"`
}

type createRequest struct {
	Project string `json:"project"`
}

// Handler is the broker's HTTP handler. It expects a verified client
// certificate on the request TLS state.
type Handler struct {
	Trust   TrustMapper
	Creator ProjectCreator
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v2/projects" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		http.Error(w, "client certificate required", http.StatusUnauthorized)
		return
	}
	fingerprint := certificateFingerprint(r.TLS.PeerCertificates[0].Raw)
	principal, err := ResolvePrincipal(r.Context(), h.Trust, fingerprint)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	project := strings.TrimSpace(req.Project)
	if err := naming.ValidateNewProjectName(project); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := h.Creator.CreateTenantProject(r.Context(), principal.Tenant, project)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// ResolvePrincipal maps a fingerprint to a broker Principal via the trust mapper.
func ResolvePrincipal(ctx context.Context, mapper TrustMapper, fingerprint string) (Principal, error) {
	if mapper == nil {
		return Principal{}, fmt.Errorf("trust mapper is not configured")
	}
	p, err := mapper.PrincipalForFingerprint(ctx, fingerprint)
	if err != nil {
		return Principal{}, err
	}
	tenant := strings.TrimSpace(p.User)
	if err := naming.ValidateTenantName(tenant); err != nil {
		return Principal{}, fmt.Errorf("certificate %s maps to invalid tenant %q", fingerprint, p.User)
	}
	return Principal{Fingerprint: fingerprint, Tenant: tenant, Projects: p.Projects}, nil
}

func certificateFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// ServePlan describes how to run the broker.
type ServePlan struct {
	Address  string
	CertFile string
	KeyFile  string
}

// Serve runs the broker until ctx is cancelled. TLS requires (but does not
// verify against a CA) a client certificate; the trust mapper is the real
// authorization gate.
func Serve(ctx context.Context, plan ServePlan, handler Handler) error {
	certificate, err := tls.LoadX509KeyPair(plan.CertFile, plan.KeyFile)
	if err != nil {
		return fmt.Errorf("load broker TLS certificate: %w", err)
	}
	address := strings.TrimSpace(plan.Address)
	if address == "" {
		address = ":9443"
	}
	listener, err := tls.Listen("tcp", address, &tls.Config{
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		return fmt.Errorf("listen for project broker on %s: %w", address, err)
	}
	server := &http.Server{
		Handler:     handler,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
