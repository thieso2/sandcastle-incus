package routebroker

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
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
	Resolver      route.DNSResolver
	Trust         TrustMapper
}

type addRequest struct {
	Hostname        string `json:"hostname"`
	TargetReference string `json:"targetReference"`
}

const maxAddRequestBytes = 4096

func (s Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/routes":
		s.handleList(w, r)
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
	request, err := decodeAddRequest(w, r)
	if err != nil {
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
	verifiedProof, err := route.VerifyDNSProof(r.Context(), s.Resolver, plan.DNSProof)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	plan.DNSProof = verifiedProof
	plan, err = stampRouteCreator(plan, principal.Owner)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if s.Routes == nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("route manager is required"))
		return
	}
	if err := s.Routes.Add(r.Context(), plan); err != nil {
		writeError(w, routeMutationErrorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, plan)
}

func decodeAddRequest(w http.ResponseWriter, r *http.Request) (addRequest, error) {
	var request addRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAddRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return addRequest{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return addRequest{}, fmt.Errorf("route add request must contain a single JSON object")
	}
	return request, nil
}

func stampRouteCreator(plan route.AddPlan, owner string) (route.AddPlan, error) {
	metadata, err := meta.ParseRouteConfig(plan.MetadataConfig)
	if err != nil {
		return route.AddPlan{}, fmt.Errorf("parse route metadata: %w", err)
	}
	metadata.CreatedBy = owner
	metadataConfig, err := meta.RouteConfig(metadata)
	if err != nil {
		return route.AddPlan{}, err
	}
	plan.MetadataConfig = metadataConfig
	return plan, nil
}

func (s Server) handleRemove(w http.ResponseWriter, r *http.Request) {
	principal, err := s.principal(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	hostname, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/routes/"))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("decode route hostname: %w", err))
		return
	}
	if hostname == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("route hostname is required"))
		return
	}
	plan, err := route.PlanRemove(s.Admin, route.RemoveRequest{Hostname: hostname})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if s.RouteMetadata == nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("route metadata store is required"))
		return
	}
	routeMetadata, err := s.RouteMetadata.FindRoute(r.Context(), plan.Hostname)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err := AuthorizeRemove(principal, routeMetadata, plan.ProjectPrefix); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if s.Routes == nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("route manager is required"))
		return
	}
	if err := s.Routes.Remove(r.Context(), plan); err != nil {
		writeError(w, routeMutationErrorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s Server) handleList(w http.ResponseWriter, r *http.Request) {
	principal, err := s.principal(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	plan, err := route.PlanList(s.Admin)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if s.Routes == nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("route manager is required"))
		return
	}
	result, err := s.Routes.List(r.Context(), plan)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, filterRoutesForPrincipal(result, principal, s.Admin.ProjectPrefix))
}

func filterRoutesForPrincipal(result route.ListResult, principal Principal, projectPrefix string) route.ListResult {
	filtered := make([]route.Route, 0, len(result.Routes))
	prefix := principal.Owner + "/"
	for _, publicRoute := range result.Routes {
		if strings.HasPrefix(publicRoute.TargetReference, prefix) && principalCanAccessProject(principal, targetIncusProject(publicRoute.TargetReference, projectPrefix)) {
			filtered = append(filtered, publicRoute)
		}
	}
	return route.ListResult{Routes: filtered}
}

func targetIncusProject(targetReference string, projectPrefix string) string {
	parts := strings.Split(targetReference, "/")
	if len(parts) < 2 {
		return ""
	}
	incusProject, err := naming.IncusProjectNameWithPrefix(projectPrefix, naming.ProjectRef{Owner: parts[0], Project: parts[1]})
	if err != nil {
		return ""
	}
	return incusProject
}

func routeMutationErrorStatus(err error) int {
	if route.IsConflict(err) {
		return http.StatusConflict
	}
	return http.StatusBadGateway
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
