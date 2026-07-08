package authapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/projectbroker"
	"github.com/thieso2/sandcastle-incus/internal/share"
	"github.com/thieso2/sandcastle-incus/internal/svclog"
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
	SimulateGitHubToken string
	DefaultUnixUser     string
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
	SimulateGitHubToken string   `json:"-"`
	DefaultUnixUser     string   `json:"defaultUnixUser,omitempty"`
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

type ShareReconciler interface {
	ReconcileTenantShares(context.Context, tenant.Summary, bool) (share.ReconcileResult, error)
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
	ShareStore       share.Store
	ShareReconciler  ShareReconciler
	// Projects performs the privileged project scaffolding for the token-gated
	// POST /api/projects — the tunnel-friendly tenant plane (no broker port).
	Projects TenantProjectCreator
	// DNSEvents, when set, is started once and subscribes to instance lifecycle
	// events, calling notify() whenever tenant machine DNS may have changed —
	// the event-driven half of ADR-0018's registration. It should block until
	// ctx is done, reconnecting internally as needed.
	DNSEvents func(ctx context.Context, notify func())
	// DNSReconcile, when set, is invoked periodically to register tenant machine
	// DNS records (auto-registration of freeform `incus launch` machines).
	DNSReconcile func(context.Context) error
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
	defaultUnixUser := strings.TrimSpace(request.DefaultUnixUser)
	if defaultUnixUser != "" {
		if err := naming.ValidateUnixUsername(defaultUnixUser); err != nil {
			return ServePlan{}, err
		}
	}
	return ServePlan{
		Address:             address,
		DatabasePath:        databasePath,
		AuthHostname:        strings.Trim(strings.TrimSpace(request.AuthHostname), "."),
		GitHubClientID:      strings.TrimSpace(request.GitHubClientID),
		GitHubClientSecret:  strings.TrimSpace(request.GitHubClientSecret),
		BootstrapAdminUsers: NormalizeGitHubUsernames(request.BootstrapAdminUsers),
		DebugDeviceUser:     NormalizeGitHubUsername(request.DebugDeviceUser),
		SimulateGitHubToken: strings.TrimSpace(request.SimulateGitHubToken),
		DefaultUnixUser:     defaultUnixUser,
		TailscaleAuthKey:    strings.TrimSpace(request.TailscaleAuthKey),
	}, nil
}

func (r HTTPRunner) Serve(ctx context.Context, plan ServePlan) error {
	db, err := OpenDatabase(plan.DatabasePath)
	if err != nil {
		return err
	}
	defer db.Close()

	// Verbose logging: every request + work span is written to stderr (journald
	// under systemd) and persisted to the logs table via the async sink so the
	// /logs browser can scope them per user.
	sink := newDBSink(db, 0)
	defer sink.Close()
	logger := svclog.New("auth-app", os.Stderr, sink)

	migrateStart := time.Now()
	if err := Migrate(ctx, db); err != nil {
		return err
	}
	logger.Message(ctx, "INFO", "auth database migrated in %dms", time.Since(migrateStart).Milliseconds())
	if err := BootstrapAdmins(ctx, db, plan.BootstrapAdminUsers); err != nil {
		return err
	}
	provisioner := r.Provisioner
	if typed, ok := provisioner.(Provisioner); ok && strings.TrimSpace(typed.DefaultUnixUser) == "" {
		typed.DefaultUnixUser = plan.DefaultUnixUser
		provisioner = typed
	}
	server := &http.Server{
		Addr: plan.Address,
		Handler: logger.HTTP(NewHandler(db, HandlerOptions{
			AuthHostname:        plan.AuthHostname,
			GitHubClientID:      plan.GitHubClientID,
			GitHubClientSecret:  plan.GitHubClientSecret,
			RestrictedUsers:     r.RestrictedUsers,
			Provisioner:         provisioner,
			Admin:               r.Admin,
			Tenants:             r.Tenants,
			TenantAccess:        r.TenantAccess,
			Machines:            r.Machines,
			MachineSSHKeys:      r.MachineSSHKeys,
			TenantSSHKeys:       r.TenantSSHKeys,
			MachineSSHAccess:    r.MachineSSHAccess,
			ShareStore:          r.ShareStore,
			ShareReconciler:     r.ShareReconciler,
			Projects:            r.Projects,
			DebugDeviceUser:     plan.DebugDeviceUser,
			SimulateGitHubToken: plan.SimulateGitHubToken,
			TailscaleAuthKey:    plan.TailscaleAuthKey,
		})),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if r.DNSReconcile != nil {
		go r.runDNSReconcileLoop(ctx, logger)
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

// runDNSReconcileLoop registers tenant machine DNS records so freeform
// `incus launch` machines resolve without a manual step (ADR-0018): instance
// lifecycle events trigger a reconcile within seconds (with two settle passes
// to catch the DHCP lease landing after the event), and a periodic pass every
// 30s guarantees convergence across missed events and restarts. Errors are
// logged and the loop continues; it stops when ctx is cancelled.
func (r HTTPRunner) runDNSReconcileLoop(ctx context.Context, logger *svclog.Logger) {
	const interval = 30 * time.Second
	reconcile := func() {
		if err := r.DNSReconcile(ctx); err != nil {
			logger.Message(ctx, "ERROR", "auth-app DNS reconcile: %v", err)
		}
	}
	if r.DNSEvents != nil {
		trigger := make(chan struct{}, 1)
		go r.DNSEvents(ctx, func() {
			select {
			case trigger <- struct{}{}:
			default:
			}
		})
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-trigger:
					reconcile()
					// The event usually precedes the DHCP lease; settle passes
					// pick up the IP. The reconciler skips unchanged zones, so
					// extra passes are cheap.
					for _, settle := range []time.Duration{3 * time.Second, 8 * time.Second} {
						select {
						case <-ctx.Done():
							return
						case <-time.After(settle):
						}
						select {
						case <-trigger: // coalesce triggers that arrived meanwhile
						default:
						}
						reconcile()
					}
				}
			}
		}()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	reconcile()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}

func OpenDatabase(path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("auth database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create auth database directory: %w", err)
	}
	// Pragmas go in the DSN so they apply to EVERY pooled connection, not just
	// the one an Exec happens to run on. WAL + busy_timeout let concurrent
	// writers (device poll, svclog sink, reconcilers) wait instead of failing
	// with SQLITE_BUSY.
	db, err := sql.Open("sqlite", path+
		"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, fmt.Errorf("open auth database: %w", err)
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
CREATE TABLE IF NOT EXISTS cli_tokens (
    id TEXT PRIMARY KEY,
    user_key TEXT NOT NULL REFERENCES users(user_key) ON DELETE CASCADE,
    token_verifier TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    last_used_at TEXT NOT NULL DEFAULT ''
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
    tenant TEXT NOT NULL DEFAULT '',
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
    tenant TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    provider TEXT NOT NULL,
    gcp_audience TEXT NOT NULL DEFAULT '',
    gcp_subject_token_type TEXT NOT NULL DEFAULT '',
    gcp_service_account_impersonation_url TEXT NOT NULL DEFAULT '',
    deleted INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(user_key, tenant, name)
);
CREATE TABLE IF NOT EXISTS logs (
    id TEXT PRIMARY KEY,
    ts TEXT NOT NULL,
    level TEXT NOT NULL,
    kind TEXT NOT NULL,
    service TEXT NOT NULL,
    event TEXT NOT NULL DEFAULT '',
    request_id TEXT NOT NULL DEFAULT '',
    user_key TEXT NOT NULL DEFAULT '',
    method TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    status INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS logs_user_ts ON logs(user_key, ts);
CREATE INDEX IF NOT EXISTS logs_ts ON logs(ts);
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
	if err := ensureColumn(ctx, db, "oidc_signing_keys", "tenant", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "cloud_identity_configs", "tenant", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := migrateCloudIdentityConfigsTenantScope(ctx, db); err != nil {
		return err
	}
	return nil
}

func migrateCloudIdentityConfigsTenantScope(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `
SELECT id, gcp_audience
FROM cloud_identity_configs
WHERE tenant = '' AND gcp_audience LIKE '%/workloadIdentityPools/sandcastle-%/providers/%'
`)
	if err != nil {
		return err
	}
	var inferred []struct {
		id     string
		tenant string
	}
	for rows.Next() {
		var id, audience string
		if err := rows.Scan(&id, &audience); err != nil {
			_ = rows.Close()
			return err
		}
		if tenant := inferTenantFromGCPAudience(audience); tenant != "" {
			inferred = append(inferred, struct {
				id     string
				tenant string
			}{id: id, tenant: tenant})
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range inferred {
		if _, err := db.ExecContext(ctx, "UPDATE cloud_identity_configs SET tenant = ? WHERE id = ?", item.tenant, item.id); err != nil {
			return err
		}
	}

	var createSQL string
	err = db.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'cloud_identity_configs'").Scan(&createSQL)
	if err != nil {
		return err
	}
	if strings.Contains(createSQL, "UNIQUE(user_key, tenant, name)") {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
CREATE TABLE cloud_identity_configs_new (
    id TEXT PRIMARY KEY,
    user_key TEXT NOT NULL,
    tenant TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    provider TEXT NOT NULL,
    gcp_audience TEXT NOT NULL DEFAULT '',
    gcp_subject_token_type TEXT NOT NULL DEFAULT '',
    gcp_service_account_impersonation_url TEXT NOT NULL DEFAULT '',
    deleted INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(user_key, tenant, name)
);
INSERT INTO cloud_identity_configs_new (
    id, user_key, tenant, name, provider, gcp_audience, gcp_subject_token_type,
    gcp_service_account_impersonation_url, deleted, created_at, updated_at
)
SELECT id, user_key, tenant, name, provider, gcp_audience, gcp_subject_token_type,
    gcp_service_account_impersonation_url, deleted, created_at, updated_at
FROM cloud_identity_configs;
DROP TABLE cloud_identity_configs;
ALTER TABLE cloud_identity_configs_new RENAME TO cloud_identity_configs;
`); err != nil {
		return fmt.Errorf("migrate cloud identity configs tenant scope: %w", err)
	}
	return nil
}

func inferTenantFromGCPAudience(audience string) string {
	const marker = "/workloadIdentityPools/sandcastle-"
	index := strings.Index(audience, marker)
	if index < 0 {
		return ""
	}
	rest := audience[index+len(marker):]
	tenant, _, ok := strings.Cut(rest, "/providers/")
	if !ok {
		return ""
	}
	return strings.TrimSpace(tenant)
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
	ShareStore         share.Store
	ShareReconciler    ShareReconciler
	// Projects performs the privileged project scaffolding for the token-gated
	// POST /api/projects — the tunnel-friendly tenant plane (no broker port).
	Projects            TenantProjectCreator
	DebugDeviceUser     string
	SimulateGitHubToken string
	TailscaleAuthKey    string
}

// TenantProjectCreator creates an app project for a tenant and extends the
// tenant's restricted certificate; satisfied by incusx.ProjectBrokerCreator.
type TenantProjectCreator interface {
	CreateTenantProject(ctx context.Context, tenant string, project string) (projectbroker.ProjectResult, error)
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
		shareStore:       handlerOptions.ShareStore,
		shareReconciler:  handlerOptions.ShareReconciler,
		projects:         handlerOptions.Projects,
		debugDeviceUser:  NormalizeGitHubUsername(handlerOptions.DebugDeviceUser),
		simulateToken:    strings.TrimSpace(handlerOptions.SimulateGitHubToken),
		tailscaleAuthKey: strings.TrimSpace(handlerOptions.TailscaleAuthKey),
		sessionCookie:    "sandcastle_session",
	}
	if app.githubClient == nil {
		if app.simulateToken != "" {
			// No real GitHub OAuth app: fabricate profiles offline.
			app.githubClient = SimulatedGitHubClient{}
		} else {
			app.githubClient = HTTPGitHubClient{}
		}
	}
	mux.HandleFunc("/", app.status)
	mux.HandleFunc("/healthz", app.health)
	mux.HandleFunc("/machines", app.machinesWeb)
	mux.HandleFunc("/login/github", app.githubLogin)
	mux.HandleFunc("/oauth/github/callback", app.githubCallback)
	mux.HandleFunc("/admin/allowlist", app.adminAllowlist)
	mux.HandleFunc("/admin/allowlist/remove", app.adminAllowlistRemove)
	mux.HandleFunc("/admin/access", app.adminTenantAccess)
	mux.HandleFunc("/admin/access/grant", app.adminTenantAccessGrant)
	mux.HandleFunc("/admin/access/revoke", app.adminTenantAccessRevoke)
	mux.HandleFunc("/cloud-identities", app.cloudIdentities)
	mux.HandleFunc("/cloud-identities/delete", app.cloudIdentityDelete)
	mux.HandleFunc("/logs", app.logsWeb)
	mux.HandleFunc("/api/cloud-identities", app.cloudIdentitiesAPI)
	mux.HandleFunc("/api/tenants", app.tenantsAPI)
	mux.HandleFunc("/api/projects", app.projectsAPI)
	mux.HandleFunc("/api/shares", app.sharesAPI)
	mux.HandleFunc("/api/shares/status", app.shareStatusAPI)
	mux.HandleFunc("/api/shares/accept", app.shareAcceptAPI)
	mux.HandleFunc("/api/shares/decline", app.shareDeclineAPI)
	mux.HandleFunc("/api/shares/revoke", app.shareRevokeAPI)
	mux.HandleFunc("/api/shares/delete", app.shareDeleteAPI)
	mux.HandleFunc("/api/shares/reconcile", app.shareReconcileAPI)
	mux.HandleFunc("/api/device/start", app.deviceStart)
	mux.HandleFunc("/api/device/poll", app.devicePoll)
	mux.HandleFunc("/api/workload/enable", app.workloadEnable)
	mux.HandleFunc("/device", app.deviceApprove)
	if app.debugDeviceUser != "" {
		mux.HandleFunc("/debug/device/approve", app.debugDeviceApprove)
	}
	if app.simulateToken != "" {
		mux.HandleFunc("/oauth/github/simulate", app.simulateLogin)
		log.Printf("auth-app WARNING: simulated GitHub mode ENABLED — /oauth/github/simulate accepts a shared token and auto-allowlists any user; DO NOT run this in production")
	}
	mux.HandleFunc("/.well-known/openid-configuration", app.oidcDiscovery)
	mux.HandleFunc("/.well-known/jwks.json", app.oidcJWKS)
	mux.HandleFunc("/t/", app.tenantOIDC)
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
	shareStore       share.Store
	shareReconciler  ShareReconciler
	projects         TenantProjectCreator
	debugDeviceUser  string
	simulateToken    string
	tailscaleAuthKey string
	sessionCookie    string
}

// projectsAPI is the tunnel-friendly tenant plane for project creation
// (ADR-0016 amended): POST /api/projects {"project": "..."} authenticated by
// the CLI Auth Token. It replaces the broker's host port in tunnel-fronted
// installs — the auth-app performs the same scaffolding + cert extension.
func (h handler) projectsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.projects == nil {
		http.Error(w, "project creation is not available on this deployment", http.StatusNotImplemented)
		return
	}
	user, err := h.requireBearerUser(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var request struct {
		Project string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	project := strings.TrimSpace(request.Project)
	if err := naming.ValidateNewProjectName(project); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var result projectbroker.ProjectResult
	err = svclog.Span(r.Context(), "project.create", func() error {
		var createErr error
		result, createErr = h.projects.CreateTenantProject(r.Context(), user.UserKey, project)
		return createErr
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
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
      <p><a href="/machines">View your machines</a></p>
      <p><a href="/logs">Activity log</a></p>
    </section>
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
