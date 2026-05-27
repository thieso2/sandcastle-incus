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

type ShareRecipientRequest struct {
	Tenant        string `json:"tenant,omitempty"`
	SourceTenant  string `json:"source_tenant"`
	SourceProject string `json:"source_project"`
	Name          string `json:"name"`
	DryRun        bool   `json:"dry_run,omitempty"`
}

type ShareReconcileRequest struct {
	Tenant string `json:"tenant,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type ShareRevokeRequest struct {
	Tenant          string `json:"tenant,omitempty"`
	Project         string `json:"project"`
	Name            string `json:"name"`
	RecipientTenant string `json:"recipient_tenant"`
	DryRun          bool   `json:"dry_run,omitempty"`
}

type ShareDeleteRequest struct {
	Tenant  string `json:"tenant,omitempty"`
	Project string `json:"project"`
	Name    string `json:"name"`
	DryRun  bool   `json:"dry_run,omitempty"`
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
	direction := strings.TrimSpace(r.URL.Query().Get("direction"))
	var result share.Result
	var err error
	switch direction {
	case "inbound":
		result, err = share.ListInbound(r.Context(), h.tenants, h.shareStore, share.ListRequest{Tenant: tenantName, Inbound: true})
	case "offers":
		result, err = share.ListInbound(r.Context(), h.tenants, h.shareStore, share.ListRequest{Tenant: tenantName, Inbound: true, Offers: true})
	default:
		result, err = share.ListOutbound(r.Context(), h.tenants, h.shareStore, share.ListRequest{Tenant: tenantName, Outbound: true})
	}
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

func (h handler) shareAcceptAPI(w http.ResponseWriter, r *http.Request) {
	h.shareRecipientMutationAPI(w, r, share.RecipientStateAccepted)
}

func (h handler) shareDeclineAPI(w http.ResponseWriter, r *http.Request) {
	h.shareRecipientMutationAPI(w, r, share.RecipientStateDeclined)
}

func (h handler) shareRevokeAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, err := h.requireBearerUser(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if h.shareStore == nil {
		http.Error(w, "share store is not configured", http.StatusInternalServerError)
		return
	}
	var request ShareRevokeRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sourceTenant := strings.TrimSpace(request.Tenant)
	if sourceTenant == "" {
		sourceTenant = strings.TrimSpace(h.admin.Tenant)
	}
	if err := h.requireTenantAccess(r, user.UserKey, sourceTenant); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	result, err := share.RevokeRecipient(r.Context(), h.tenants, h.shareStore, share.RevokeRequest{
		SourceTenant:    sourceTenant,
		SourceProject:   request.Project,
		Name:            request.Name,
		RecipientTenant: request.RecipientTenant,
		DryRun:          request.DryRun,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.reconcileShareRecipients(w, r, &result, request.DryRun)
}

func (h handler) shareDeleteAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, err := h.requireBearerUser(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if h.shareStore == nil {
		http.Error(w, "share store is not configured", http.StatusInternalServerError)
		return
	}
	var request ShareDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sourceTenant := strings.TrimSpace(request.Tenant)
	if sourceTenant == "" {
		sourceTenant = strings.TrimSpace(h.admin.Tenant)
	}
	if err := h.requireTenantAccess(r, user.UserKey, sourceTenant); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	result, err := share.DeleteOutbound(r.Context(), h.tenants, h.shareStore, share.DeleteRequest{
		SourceTenant:  sourceTenant,
		SourceProject: request.Project,
		Name:          request.Name,
		DryRun:        request.DryRun,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.reconcileShareRecipients(w, r, &result, request.DryRun)
}

func (h handler) shareRecipientMutationAPI(w http.ResponseWriter, r *http.Request, state string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, err := h.requireBearerUser(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if h.shareStore == nil {
		http.Error(w, "share store is not configured", http.StatusInternalServerError)
		return
	}
	var request ShareRecipientRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tenantName := strings.TrimSpace(request.Tenant)
	if tenantName == "" {
		tenantName = strings.TrimSpace(h.admin.Tenant)
	}
	if err := h.requireTenantAccess(r, user.UserKey, tenantName); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	result, err := share.SetRecipientState(r.Context(), h.tenants, h.shareStore, share.RecipientRequest{
		Tenant:        tenantName,
		SourceTenant:  request.SourceTenant,
		SourceProject: request.SourceProject,
		Name:          request.Name,
		Actor:         user.UserKey,
		State:         state,
		DryRun:        request.DryRun,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if state == share.RecipientStateAccepted && h.shareReconciler != nil {
		summary, err := h.findTenantSummary(r, tenantName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if request.DryRun {
			summary.StorageShares = append(append([]meta.TenantStorageShare{}, summary.StorageShares...), result.Share)
		}
		reconcile, err := h.shareReconciler.ReconcileTenantShares(r.Context(), summary, request.DryRun)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		result.Reconcile = &reconcile
	}
	writeJSON(w, http.StatusOK, result)
}

func (h handler) reconcileShareRecipients(w http.ResponseWriter, r *http.Request, result *share.Result, dryRun bool) {
	if h.shareReconciler != nil {
		for _, recipient := range result.AffectedRecipients {
			summary, err := h.findTenantSummary(r, recipient)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if dryRun {
				summary.StorageShares = share.RemoveShare(summary.StorageShares, result.Share.SourceTenant, result.Share.SourceProject, result.Share.Name)
			}
			reconcile, err := h.shareReconciler.ReconcileTenantShares(r.Context(), summary, dryRun)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			result.Reconciles = append(result.Reconciles, reconcile)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (h handler) shareReconcileAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, err := h.requireBearerUser(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if h.shareReconciler == nil {
		http.Error(w, "share reconciler is not configured", http.StatusInternalServerError)
		return
	}
	var request ShareReconcileRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tenantName := strings.TrimSpace(request.Tenant)
	if tenantName == "" {
		tenantName = strings.TrimSpace(h.admin.Tenant)
	}
	if err := h.requireTenantAccess(r, user.UserKey, tenantName); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	summary, err := h.findTenantSummary(r, tenantName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := h.shareReconciler.ReconcileTenantShares(r.Context(), summary, request.DryRun)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, result)
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
