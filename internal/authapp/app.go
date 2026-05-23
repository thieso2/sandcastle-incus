package authapp

import (
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
	_ "modernc.org/sqlite"
)

type ServeRequest struct {
	Address             string
	DatabasePath        string
	AuthHostname        string
	GitHubClientID      string
	GitHubClientSecret  string
	BootstrapAdminUsers []string
	DebugDeviceUser     string
	TailscaleAuthKey    string
}

type ServePlan struct {
	Address             string   `json:"address"`
	DatabasePath        string   `json:"databasePath"`
	AuthHostname        string   `json:"authHostname"`
	GitHubClientID      string   `json:"githubClientID,omitempty"`
	GitHubClientSecret  string   `json:"-"`
	BootstrapAdminUsers []string `json:"bootstrapAdminUsers,omitempty"`
	DebugDeviceUser     string   `json:"debugDeviceUser,omitempty"`
	TailscaleAuthKey    string   `json:"-"`
}

type Runner interface {
	Serve(context.Context, ServePlan) error
}

type RestrictedUserRevoker interface {
	Delete(context.Context, usertrust.UserPlan) error
}

type TenantAccessManager interface {
	Grant(context.Context, usertrust.UserPlan) error
	Revoke(context.Context, usertrust.UserPlan) error
	ListTenantUsers(context.Context, usertrust.TenantUsersPlan) (usertrust.TenantUsersResult, error)
}

type MachineSSHKeyReconciler interface {
	ReconcileUserSSHKey(context.Context, tenant.Summary, string, string) error
}

type TenantSSHKeyUpdater interface {
	SetTenantSSHKey(context.Context, string, string) error
}

type MachineSSHAccessRevoker interface {
	RevokeUserSSHKey(context.Context, tenant.Summary, string) error
}

type HTTPRunner struct {
	RestrictedUsers  RestrictedUserRevoker
	Provisioner      PersonalTenantProvisioner
	Admin            config.Admin
	Tenants          tenant.IncusTenantStore
	TenantAccess     TenantAccessManager
	Machines         machine.Store
	MachineSSHKeys   MachineSSHKeyReconciler
	TenantSSHKeys    TenantSSHKeyUpdater
	MachineSSHAccess MachineSSHAccessRevoker
}

func PlanServe(request ServeRequest) (ServePlan, error) {
	address := strings.TrimSpace(request.Address)
	if address == "" {
		return ServePlan{}, fmt.Errorf("auth app listen address is required")
	}
	databasePath := strings.TrimSpace(request.DatabasePath)
	if databasePath == "" {
		return ServePlan{}, fmt.Errorf("auth database path is required")
	}
	return ServePlan{
		Address:             address,
		DatabasePath:        databasePath,
		AuthHostname:        strings.Trim(strings.TrimSpace(request.AuthHostname), "."),
		GitHubClientID:      strings.TrimSpace(request.GitHubClientID),
		GitHubClientSecret:  strings.TrimSpace(request.GitHubClientSecret),
		BootstrapAdminUsers: NormalizeGitHubUsernames(request.BootstrapAdminUsers),
		DebugDeviceUser:     NormalizeGitHubUsername(request.DebugDeviceUser),
		TailscaleAuthKey:    strings.TrimSpace(request.TailscaleAuthKey),
	}, nil
}

func (r HTTPRunner) Serve(ctx context.Context, plan ServePlan) error {
	db, err := OpenDatabase(plan.DatabasePath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := Migrate(ctx, db); err != nil {
		return err
	}
	if err := BootstrapAdmins(ctx, db, plan.BootstrapAdminUsers); err != nil {
		return err
	}
	server := &http.Server{
		Addr: plan.Address,
		Handler: NewHandler(db, HandlerOptions{
			AuthHostname:       plan.AuthHostname,
			GitHubClientID:     plan.GitHubClientID,
			GitHubClientSecret: plan.GitHubClientSecret,
			RestrictedUsers:    r.RestrictedUsers,
			Provisioner:        r.Provisioner,
			Admin:              r.Admin,
			Tenants:            r.Tenants,
			TenantAccess:       r.TenantAccess,
			Machines:           r.Machines,
			MachineSSHKeys:     r.MachineSSHKeys,
			TenantSSHKeys:      r.TenantSSHKeys,
			MachineSSHAccess:   r.MachineSSHAccess,
			DebugDeviceUser:    plan.DebugDeviceUser,
			TailscaleAuthKey:   plan.TailscaleAuthKey,
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	case err := <-errCh:
		return err
	}
}

func OpenDatabase(path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("auth database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create auth database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open auth database: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure auth database: %w", err)
	}
	return db, nil
}

func Migrate(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("auth database is required")
	}
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS auth_app_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS users (
    user_key TEXT PRIMARY KEY,
    github_username TEXT NOT NULL,
    github_username_normalized TEXT NOT NULL UNIQUE,
    github_account_id TEXT NOT NULL DEFAULT '',
    github_email TEXT NOT NULL DEFAULT '',
    ssh_public_key TEXT NOT NULL DEFAULT '',
    ssh_key_fingerprint TEXT NOT NULL DEFAULT '',
    allowlisted INTEGER NOT NULL DEFAULT 0,
    sandcastle_admin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS web_sessions (
    id TEXT PRIMARY KEY,
    user_key TEXT NOT NULL REFERENCES users(user_key) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS oauth_states (
    state TEXT PRIMARY KEY,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS device_logins (
    device_code TEXT PRIMARY KEY,
    user_code TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL,
    user_key TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    provisioned_at TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    approved_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS oidc_signing_keys (
    kid TEXT PRIMARY KEY,
    alg TEXT NOT NULL,
    encrypted_private_key TEXT NOT NULL,
    public_jwk TEXT NOT NULL,
    active INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    not_before TEXT NOT NULL,
    retired_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS machine_runtime_secrets (
    tenant TEXT NOT NULL,
    project TEXT NOT NULL,
    machine TEXT NOT NULL,
    user_key TEXT NOT NULL,
    github_username TEXT NOT NULL DEFAULT '',
    secret_verifier TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    rotated_at TEXT NOT NULL,
    PRIMARY KEY (tenant, project, machine)
);
CREATE TABLE IF NOT EXISTS cloud_identity_configs (
    id TEXT PRIMARY KEY,
    user_key TEXT NOT NULL,
    name TEXT NOT NULL,
    provider TEXT NOT NULL,
    gcp_audience TEXT NOT NULL DEFAULT '',
    gcp_subject_token_type TEXT NOT NULL DEFAULT '',
    gcp_service_account_impersonation_url TEXT NOT NULL DEFAULT '',
    deleted INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(user_key, name)
);
INSERT INTO auth_app_meta (key, value, updated_at)
VALUES ('schema_version', '1', datetime('now'))
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;
`); err != nil {
		return fmt.Errorf("migrate auth database: %w", err)
	}
	if err := ensureColumn(ctx, db, "device_logins", "provisioned_at", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "users", "ssh_public_key", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "users", "ssh_key_fingerprint", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

func ensureColumn(ctx context.Context, db *sql.DB, table string, column string, definition string) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+column+" "+definition); err != nil {
		return fmt.Errorf("add %s.%s column: %w", table, column, err)
	}
	return nil
}

type HandlerOptions struct {
	AuthHostname       string
	GitHubClientID     string
	GitHubClientSecret string
	GitHub             GitHubClient
	RestrictedUsers    RestrictedUserRevoker
	Provisioner        PersonalTenantProvisioner
	Admin              config.Admin
	Tenants            tenant.IncusTenantStore
	TenantAccess       TenantAccessManager
	Machines           machine.Store
	MachineSSHKeys     MachineSSHKeyReconciler
	TenantSSHKeys      TenantSSHKeyUpdater
	MachineSSHAccess   MachineSSHAccessRevoker
	DebugDeviceUser    string
	TailscaleAuthKey   string
}

func NewHandler(db *sql.DB, options any) http.Handler {
	mux := http.NewServeMux()
	handlerOptions := normalizeHandlerOptions(options)
	app := handler{
		db:               db,
		authHostname:     strings.Trim(strings.TrimSpace(handlerOptions.AuthHostname), "."),
		githubClient:     handlerOptions.GitHub,
		githubOAuth:      GitHubOAuth{ClientID: handlerOptions.GitHubClientID, ClientSecret: handlerOptions.GitHubClientSecret},
		restricted:       handlerOptions.RestrictedUsers,
		provisioner:      handlerOptions.Provisioner,
		admin:            handlerOptions.Admin,
		tenants:          handlerOptions.Tenants,
		tenantAccess:     handlerOptions.TenantAccess,
		machines:         handlerOptions.Machines,
		machineSSHKeys:   handlerOptions.MachineSSHKeys,
		tenantSSHKeys:    handlerOptions.TenantSSHKeys,
		machineSSHAccess: handlerOptions.MachineSSHAccess,
		debugDeviceUser:  NormalizeGitHubUsername(handlerOptions.DebugDeviceUser),
		tailscaleAuthKey: strings.TrimSpace(handlerOptions.TailscaleAuthKey),
		sessionCookie:    "sandcastle_session",
	}
	if app.githubClient == nil {
		app.githubClient = HTTPGitHubClient{}
	}
	mux.HandleFunc("/", app.status)
	mux.HandleFunc("/healthz", app.health)
	mux.HandleFunc("/login/github", app.githubLogin)
	mux.HandleFunc("/oauth/github/callback", app.githubCallback)
	mux.HandleFunc("/admin/allowlist", app.adminAllowlist)
	mux.HandleFunc("/admin/allowlist/remove", app.adminAllowlistRemove)
	mux.HandleFunc("/admin/access", app.adminTenantAccess)
	mux.HandleFunc("/admin/access/grant", app.adminTenantAccessGrant)
	mux.HandleFunc("/admin/access/revoke", app.adminTenantAccessRevoke)
	mux.HandleFunc("/cloud-identities", app.cloudIdentities)
	mux.HandleFunc("/cloud-identities/delete", app.cloudIdentityDelete)
	mux.HandleFunc("/api/device/start", app.deviceStart)
	mux.HandleFunc("/api/device/poll", app.devicePoll)
	mux.HandleFunc("/device", app.deviceApprove)
	if app.debugDeviceUser != "" {
		mux.HandleFunc("/debug/device/approve", app.debugDeviceApprove)
	}
	mux.HandleFunc("/.well-known/openid-configuration", app.oidcDiscovery)
	mux.HandleFunc("/.well-known/jwks.json", app.oidcJWKS)
	mux.HandleFunc("/internal/workload/token", app.workloadToken)
	return mux
}

func normalizeHandlerOptions(value any) HandlerOptions {
	switch typed := value.(type) {
	case HandlerOptions:
		return typed
	case string:
		return HandlerOptions{AuthHostname: typed}
	default:
		return HandlerOptions{}
	}
}

type handler struct {
	db               *sql.DB
	authHostname     string
	githubClient     GitHubClient
	githubOAuth      GitHubOAuth
	restricted       RestrictedUserRevoker
	provisioner      PersonalTenantProvisioner
	admin            config.Admin
	tenants          tenant.IncusTenantStore
	tenantAccess     TenantAccessManager
	machines         machine.Store
	machineSSHKeys   MachineSSHKeyReconciler
	tenantSSHKeys    TenantSSHKeyUpdater
	machineSSHAccess MachineSSHAccessRevoker
	debugDeviceUser  string
	tailscaleAuthKey string
	sessionCookie    string
}

func (h handler) health(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/healthz" {
		http.NotFound(w, r)
		return
	}
	if err := h.db.PingContext(r.Context()); err != nil {
		http.Error(w, "auth database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (h handler) status(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if err := h.db.PingContext(r.Context()); err != nil {
		http.Error(w, "auth database unavailable", http.StatusServiceUnavailable)
		return
	}
	if cookie, err := r.Cookie(h.sessionCookie); err == nil {
		if user, err := UserForSession(r.Context(), h.db, cookie.Value, timeNow()); err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = onboardingTemplate.Execute(w, onboardingPage{
				User:         user,
				AuthHostname: h.authHostname,
				LoginCommand: cliLoginCommand(h.authHostname),
			})
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = statusTemplate.Execute(w, struct {
		AuthHostname string
	}{AuthHostname: h.authHostname})
}

type onboardingPage struct {
	User         User
	AuthHostname string
	LoginCommand string
}

func cliLoginCommand(authHostname string) string {
	host := strings.TrimSpace(authHostname)
	if host == "" {
		return "sandcastle login <auth-host>"
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return "sandcastle login " + strings.TrimRight(host, "/")
	}
	return "sandcastle login https://" + strings.Trim(host, ".")
}

var statusTemplate = template.Must(template.New("status").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Sandcastle Auth</title>
</head>
<body>
  <main>
    <h1>Sandcastle Auth</h1>
    <p>Status: ok</p>
    {{if .AuthHostname}}<p>Auth Hostname: {{.AuthHostname}}</p>{{end}}
    <p><a href="/login/github">Sign in with GitHub</a></p>
  </main>
</body>
</html>
`))

var onboardingTemplate = template.Must(template.New("onboarding").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Sandcastle Onboarding</title>
</head>
<body>
  <main>
    <h1>Sandcastle Onboarding</h1>
    <section>
      <h2>GitHub identity</h2>
      <p>GitHub Username: {{.User.GitHubUsername}}</p>
      <p>Sandcastle User Key: {{.User.UserKey}}</p>
      {{if .User.GitHubEmail}}<p>GitHub Email: {{.User.GitHubEmail}}</p>{{end}}
    </section>
    <section>
      <h2>Allowlist status</h2>
      {{if .User.Allowlisted}}
        <p>Status: allowlisted</p>
      {{else}}
        <p>Status: not allowlisted</p>
        <p>Ask a Sandcastle Admin to add your GitHub Username to the Login Allowlist before running CLI Device Login.</p>
      {{end}}
    </section>
    {{if .User.Allowlisted}}
      <section>
        <h2>Install the CLI</h2>
        <p>Install the Sandcastle CLI for your platform, then run the login command from a terminal.</p>
        <pre><code>mise run build
mise run build:linux-amd64</code></pre>
      </section>
      <section>
        <h2>Login command</h2>
        <pre><code>{{.LoginCommand}}</code></pre>
      </section>
    {{end}}
  </main>
</body>
</html>
`))
