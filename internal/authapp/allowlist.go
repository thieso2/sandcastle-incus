package authapp

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/svclog"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

var timeNow = time.Now

func (h handler) requireAdmin(r *http.Request) (User, error) {
	cookie, err := r.Cookie(h.sessionCookie)
	if err != nil {
		return User{}, fmt.Errorf("web session is required")
	}
	user, err := UserForSession(r.Context(), h.db, cookie.Value, timeNow())
	if err != nil {
		return User{}, err
	}
	if !user.SandcastleAdmin {
		return User{}, fmt.Errorf("Sandcastle Admin access is required")
	}
	svclog.SetUser(r.Context(), user.UserKey)
	return user, nil
}

func (h handler) adminAllowlist(w http.ResponseWriter, r *http.Request) {
	if _, err := h.requireAdmin(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodGet:
		users, err := ListUsers(r.Context(), h.db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = allowlistTemplate.Execute(w, users)
	case http.MethodPost:
		username := strings.TrimSpace(r.FormValue("github_username"))
		if err := ValidateGitHubUsername(username); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		profile, err := h.githubClient.VerifyUsername(r.Context(), username)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := AllowlistGitHubUser(r.Context(), h.db, profile); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/admin/allowlist", http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h handler) adminAllowlistRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := h.requireAdmin(r); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	username := strings.TrimSpace(r.FormValue("github_username"))
	user, err := RemoveAllowlistedUser(r.Context(), h.db, username)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.restricted != nil {
		plan, err := usertrust.PlanDeleteUser(user.UserKey)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := h.restricted.Delete(r.Context(), plan); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
	}
	if err := h.revokeMachineSSHAccessFromAllTenants(r, user.UserKey); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/admin/allowlist", http.StatusSeeOther)
}

func (h handler) revokeMachineSSHAccessFromAllTenants(r *http.Request, userKey string) error {
	if h.machineSSHAccess == nil {
		return nil
	}
	if h.tenants == nil {
		return fmt.Errorf("tenant store is not configured")
	}
	summaries, err := tenant.ListForPrefix(r.Context(), h.tenants, h.admin.IncusProjectPrefix)
	if err != nil {
		return err
	}
	for _, summary := range summaries {
		if err := h.machineSSHAccess.RevokeUserSSHKey(r.Context(), summary, NormalizeGitHubUsername(userKey)); err != nil {
			return err
		}
	}
	return nil
}

var allowlistTemplate = template.Must(template.New("allowlist").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Sandcastle Login Allowlist</title>
</head>
<body>
  <main>
    <h1>Login Allowlist</h1>
    <p><a href="/">Home</a> · <a href="/logs">Activity log</a> · <a href="/admin/access">Tenant Access</a></p>
    <form method="post" action="/admin/allowlist">
      <label>GitHub Username <input name="github_username"></label>
      <button type="submit">Add</button>
    </form>
    <table>
      <thead><tr><th>User</th><th>GitHub ID</th><th>Email</th><th>Admin</th><th>Allowlisted</th></tr></thead>
      <tbody>
        {{range .}}
        <tr>
          <td>{{.GitHubUsernameNormalized}}</td>
          <td>{{.GitHubAccountID}}</td>
          <td>{{.GitHubEmail}}</td>
          <td>{{.SandcastleAdmin}}</td>
          <td>{{.Allowlisted}}</td>
        </tr>
        {{end}}
      </tbody>
    </table>
  </main>
</body>
</html>
`))
