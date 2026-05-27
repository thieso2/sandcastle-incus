package authapp

import (
	"fmt"
	"net/http"

	"github.com/thieso2/sandcastle-incus/internal/tenant"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

type TenantAccessSummary struct {
	Tenant   string `json:"tenant"`
	Personal bool   `json:"personal"`
}

type TenantAccessListResult struct {
	Tenants []TenantAccessSummary `json:"tenants"`
}

func (h handler) tenantsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, err := h.requireBearerUser(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if h.tenants == nil {
		http.Error(w, "tenant store is not configured", http.StatusInternalServerError)
		return
	}
	if h.tenantAccess == nil {
		http.Error(w, "tenant access manager is not configured", http.StatusInternalServerError)
		return
	}
	summaries, err := tenant.List(r.Context(), h.tenants)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	result := TenantAccessListResult{}
	normalized := NormalizeGitHubUsername(user.UserKey)
	for _, summary := range summaries {
		plan, err := usertrust.PlanTenantUsersForRequest(h.admin, usertrust.TenantAccessRequest{Tenant: summary.Tenant, Personal: summary.Personal})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		users, err := h.tenantAccess.ListTenantUsers(r.Context(), plan)
		if err != nil {
			http.Error(w, fmt.Sprintf("list tenant users for %s: %v", summary.Tenant, err), http.StatusBadGateway)
			return
		}
		if containsNormalizedUser(users.Users, normalized) {
			result.Tenants = append(result.Tenants, TenantAccessSummary{Tenant: summary.Tenant, Personal: summary.Personal})
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func containsNormalizedUser(users []string, userKey string) bool {
	for _, candidate := range users {
		if NormalizeGitHubUsername(candidate) == userKey {
			return true
		}
	}
	return false
}
