package authapp

import (
	"net/http"
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
	summaries, err := h.accessibleTenantSummaries(r, user)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	result := TenantAccessListResult{}
	for _, summary := range summaries {
		result.Tenants = append(result.Tenants, TenantAccessSummary{Tenant: summary.Tenant, Personal: summary.Personal})
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
