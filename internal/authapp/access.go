package authapp

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/tenant"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

type accessTenant struct {
	Tenant   string
	Personal bool
	Users    []string
}

type accessPage struct {
	Users   []User
	Tenants []accessTenant
}

func (h handler) adminTenantAccess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := h.requireAdmin(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	page, err := h.tenantAccessPage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tenantAccessTemplate.Execute(w, page)
}

func (h handler) adminTenantAccessGrant(w http.ResponseWriter, r *http.Request) {
	h.adminTenantAccessMutation(w, r, "grant")
}

func (h handler) adminTenantAccessRevoke(w http.ResponseWriter, r *http.Request) {
	h.adminTenantAccessMutation(w, r, "revoke")
}

func (h handler) adminTenantAccessMutation(w http.ResponseWriter, r *http.Request, action string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := h.requireAdmin(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if h.tenantAccess == nil {
		http.Error(w, "tenant access manager is not configured", http.StatusInternalServerError)
		return
	}
	tenantName := strings.TrimSpace(r.FormValue("tenant"))
	userKey := NormalizeGitHubUsername(r.FormValue("user"))
	personal := strings.TrimSpace(r.FormValue("personal")) == "1"
	request := usertrust.TenantAccessRequest{Tenant: tenantName, User: userKey, Personal: personal}
	var plan usertrust.UserPlan
	var err error
	switch action {
	case "grant":
		plan, err = usertrust.PlanTenantGrant(h.admin, request)
	case "revoke":
		plan, err = usertrust.PlanTenantRevoke(h.admin, request)
	default:
		err = fmt.Errorf("unsupported tenant access action %q", action)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if action == "grant" {
		err = h.tenantAccess.Grant(r.Context(), plan)
	} else {
		err = h.tenantAccess.Revoke(r.Context(), plan)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if action == "revoke" {
		if err := h.revokeMachineSSHAccess(r, tenantName, plan.User); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
	}
	http.Redirect(w, r, "/admin/access", http.StatusSeeOther)
}

func (h handler) revokeMachineSSHAccess(r *http.Request, tenantName string, userKey string) error {
	if h.machineSSHAccess == nil {
		return nil
	}
	summary, err := h.findTenantSummary(r, tenantName)
	if err != nil {
		return err
	}
	return h.machineSSHAccess.RevokeUserSSHKey(r.Context(), summary, NormalizeGitHubUsername(userKey))
}

func (h handler) findTenantSummary(r *http.Request, tenantName string) (tenant.Summary, error) {
	if h.tenants == nil {
		return tenant.Summary{}, fmt.Errorf("tenant store is not configured")
	}
	summaries, err := tenant.List(r.Context(), h.tenants)
	if err != nil {
		return tenant.Summary{}, err
	}
	normalized := NormalizeGitHubUsername(tenantName)
	for _, summary := range summaries {
		if summary.Tenant == normalized {
			return summary, nil
		}
	}
	return tenant.Summary{}, fmt.Errorf("Sandcastle tenant %s not found", normalized)
}

func (h handler) tenantAccessPage(r *http.Request) (accessPage, error) {
	if h.tenants == nil {
		return accessPage{}, fmt.Errorf("tenant store is not configured")
	}
	if h.tenantAccess == nil {
		return accessPage{}, fmt.Errorf("tenant access manager is not configured")
	}
	users, err := ListUsers(r.Context(), h.db)
	if err != nil {
		return accessPage{}, err
	}
	summaries, err := tenant.List(r.Context(), h.tenants)
	if err != nil {
		return accessPage{}, err
	}
	page := accessPage{Users: users}
	for _, summary := range summaries {
		plan, err := usertrust.PlanTenantUsersForRequest(h.admin, usertrust.TenantAccessRequest{Tenant: summary.Tenant, Personal: summary.Personal})
		if err != nil {
			return accessPage{}, err
		}
		result, err := h.tenantAccess.ListTenantUsers(r.Context(), plan)
		if err != nil {
			return accessPage{}, err
		}
		page.Tenants = append(page.Tenants, accessTenant{
			Tenant:   summary.Tenant,
			Personal: summary.Personal,
			Users:    append([]string{}, result.Users...),
		})
	}
	return page, nil
}

var tenantAccessTemplate = template.Must(template.New("tenant-access").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Sandcastle Tenant Access</title>
</head>
<body>
  <main>
    <h1>Tenant Access</h1>
    <form method="post" action="/admin/access/grant">
      <label>User <input name="user"></label>
      <label>Tenant <input name="tenant"></label>
      <label>Personal <input type="checkbox" name="personal" value="1"></label>
      <button type="submit">Grant</button>
    </form>
    <form method="post" action="/admin/access/revoke">
      <label>User <input name="user"></label>
      <label>Tenant <input name="tenant"></label>
      <label>Personal <input type="checkbox" name="personal" value="1"></label>
      <button type="submit">Revoke</button>
    </form>
    <h2>Users</h2>
    <table>
      <thead><tr><th>User</th><th>Allowlisted</th><th>Admin</th></tr></thead>
      <tbody>
        {{range .Users}}
        <tr><td>{{.UserKey}}</td><td>{{.Allowlisted}}</td><td>{{.SandcastleAdmin}}</td></tr>
        {{end}}
      </tbody>
    </table>
    <h2>Tenants</h2>
    <table>
      <thead><tr><th>Tenant</th><th>Personal</th><th>Users</th></tr></thead>
      <tbody>
        {{range .Tenants}}
        <tr><td>{{.Tenant}}</td><td>{{.Personal}}</td><td>{{range .Users}}{{.}} {{end}}</td></tr>
        {{end}}
      </tbody>
    </table>
  </main>
</body>
</html>
`))
