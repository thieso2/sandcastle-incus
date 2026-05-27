package authapp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/share"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

type ShareCreateRequest struct {
	SourceTenant string   `json:"source_tenant,omitempty"`
	Source       string   `json:"source"`
	Recipients   []string `json:"recipients"`
	Name         string   `json:"name,omitempty"`
	DryRun       bool     `json:"dry_run,omitempty"`
}

type ShareListResult struct {
	Shares []meta.TenantStorageShare `json:"shares"`
}

func (h handler) sharesAPI(w http.ResponseWriter, r *http.Request) {
	user, err := h.requireBearerUser(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if h.shareStore == nil {
		http.Error(w, "share store is not configured", http.StatusInternalServerError)
		return
	}
	switch r.Method {
	case http.MethodPost:
		h.shareCreateAPI(w, r, user)
	case http.MethodGet:
		h.shareListAPI(w, r, user)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h handler) shareCreateAPI(w http.ResponseWriter, r *http.Request, user User) {
	var request ShareCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sourceTenant := strings.TrimSpace(request.SourceTenant)
	if sourceTenant == "" {
		sourceTenant = strings.TrimSpace(h.admin.Tenant)
	}
	if err := h.requireTenantAccess(r, user.UserKey, sourceTenant); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	result, err := share.PlanCreate(r.Context(), h.tenants, h.shareStore, share.CreateRequest{
		SourceTenant: sourceTenant,
		Source:       request.Source,
		Recipients:   request.Recipients,
		Name:         request.Name,
		Actor:        user.UserKey,
		DryRun:       request.DryRun,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, result.Share)
}

func (h handler) shareListAPI(w http.ResponseWriter, r *http.Request, user User) {
	tenantName := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenantName == "" {
		tenantName = strings.TrimSpace(h.admin.Tenant)
	}
	if err := h.requireTenantAccess(r, user.UserKey, tenantName); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	result, err := share.ListOutbound(r.Context(), h.tenants, h.shareStore, share.ListRequest{Tenant: tenantName, Outbound: true})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, ShareListResult{Shares: result.Shares})
}

func (h handler) shareStatusAPI(w http.ResponseWriter, r *http.Request) {
	user, err := h.requireBearerUser(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if h.shareStore == nil {
		http.Error(w, "share store is not configured", http.StatusInternalServerError)
		return
	}
	tenantName := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenantName == "" {
		tenantName = strings.TrimSpace(h.admin.Tenant)
	}
	if err := h.requireTenantAccess(r, user.UserKey, tenantName); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	result, err := share.GetOutbound(r.Context(), h.tenants, h.shareStore, share.StatusRequest{
		Tenant:  tenantName,
		Project: r.URL.Query().Get("project"),
		Name:    r.URL.Query().Get("name"),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, result.Share)
}

func (h handler) requireTenantAccess(r *http.Request, userKey string, tenantName string) error {
	if h.tenantAccess == nil {
		return fmt.Errorf("tenant access manager is not configured")
	}
	summary, err := h.findTenantSummary(r, tenantName)
	if err != nil {
		return err
	}
	plan, err := usertrust.PlanTenantUsersForRequest(h.admin, usertrust.TenantAccessRequest{Tenant: summary.Tenant, Personal: summary.Personal})
	if err != nil {
		return err
	}
	result, err := h.tenantAccess.ListTenantUsers(r.Context(), plan)
	if err != nil {
		return err
	}
	normalized := NormalizeGitHubUsername(userKey)
	for _, candidate := range result.Users {
		if NormalizeGitHubUsername(candidate) == normalized {
			return nil
		}
	}
	return fmt.Errorf("user %s is not granted access to tenant %s", normalized, summary.Tenant)
}
