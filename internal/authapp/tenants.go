package authapp

import (
	"net/http"
)

type TenantAccessSummary struct {
	Tenant   string `json:"tenant"`
	Personal bool   `json:"personal"`
	// PrivateCIDR is the tenant's /24 (v2) — clients use it to verify the
	// tenant tailnet path without a fresh device login.
	PrivateCIDR string `json:"private_cidr,omitempty"`
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
		result.Tenants = append(result.Tenants, TenantAccessSummary{Tenant: summary.Tenant, Personal: summary.Personal, PrivateCIDR: summary.PrivateCIDR})
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
