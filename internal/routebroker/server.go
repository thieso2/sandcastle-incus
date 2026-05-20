package routebroker

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

type RouteMetadataStore interface {
	FindRoute(ctx context.Context, hostname string) (meta.Route, error)
}

type Server struct {
	Admin         config.Admin
	Projects      project.IncusProjectStore
	Sandboxes     route.SandboxStore
	Routes        route.Manager
	RouteMetadata RouteMetadataStore
	Trust         TrustMapper
}

type addRequest struct {
	Hostname        string `json:"hostname"`
	TargetReference string `json:"targetReference"`
}

func (s Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/routes":
		s.handleAdd(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/routes/"):
		s.handleRemove(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s Server) handleAdd(w http.ResponseWriter, r *http.Request) {
	principal, err := s.principal(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	var request addRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	plan, err := route.PlanAdd(r.Context(), s.Admin, s.Projects, s.Sandboxes, route.AddRequest{
		Hostname:        request.Hostname,
		TargetReference: request.TargetReference,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := AuthorizeAdd(principal, plan); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if s.Routes == nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("route manager is required"))
		return
	}
	if err := s.Routes.Add(r.Context(), plan); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusCreated, plan)
}

func (s Server) handleRemove(w http.ResponseWriter, r *http.Request) {
	principal, err := s.principal(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	hostname := strings.TrimPrefix(r.URL.Path, "/routes/")
	if hostname == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("route hostname is required"))
		return
	}
	if s.RouteMetadata == nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("route metadata store is required"))
		return
	}
	routeMetadata, err := s.RouteMetadata.FindRoute(r.Context(), hostname)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err := AuthorizeRemove(principal, routeMetadata); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	plan, err := route.PlanRemove(s.Admin, route.RemoveRequest{Hostname: hostname})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if s.Routes == nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("route manager is required"))
		return
	}
	if err := s.Routes.Remove(r.Context(), plan); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s Server) principal(r *http.Request) (Principal, error) {
	fingerprint, err := certificateFingerprint(r)
	if err != nil {
		return Principal{}, err
	}
	return PrincipalFromFingerprint(r.Context(), s.Trust, fingerprint)
}

func certificateFingerprint(r *http.Request) (string, error) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return "", fmt.Errorf("mTLS client certificate is required")
	}
	return Fingerprint(r.TLS.PeerCertificates[0]), nil
}

func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
