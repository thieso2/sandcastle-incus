package e2e

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/authapp"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenantpkg "github.com/thieso2/sandcastle-incus/internal/tenant"
	"golang.org/x/crypto/ssh"
)

func TestFirstRunLoginWithMockGitHubOAuth(t *testing.T) {
	ctx := context.Background()
	db, err := authapp.OpenDatabase(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := authapp.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	github := authapp.NewMockGitHubOAuth(authapp.GitHubProfile{Login: "OctoCat", ID: "583231", Email: "octo@example.com"})
	if _, err := authapp.AllowlistGitHubUser(ctx, db, authapp.GitHubProfile{Login: "OctoCat", ID: "583231", Email: "octo@example.com"}); err != nil {
		t.Fatal(err)
	}
	tenantConfig, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "octocat",
		Personal:    true,
		PrivateCIDR: "10.248.1.0/24",
		Projects:    []meta.Project{{Name: "default"}},
		Tailscale:   meta.Tailscale{State: "running", Tailnet: "tailnet.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	provisioner := &firstRunProvisioner{}
	reconciler := &firstRunSSHReconciler{}
	tenantStore := tenantpkg.MemoryStore{Projects: []tenantpkg.IncusProject{{
		Name:   "sc-octocat",
		Config: tenantConfig,
	}}}
	handler := authapp.NewHandler(db, authapp.HandlerOptions{
		AuthHostname:       "auth.example.test",
		GitHubClientID:     "client",
		GitHubClientSecret: "secret",
		GitHub:             github,
		Tenants:            tenantStore,
		Provisioner:        provisioner,
		MachineSSHKeys:     reconciler,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/login/github", nil))
	if login.Code != http.StatusFound {
		t.Fatalf("login = %d %q", login.Code, login.Body.String())
	}
	location, err := url.Parse(login.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := location.Query().Get("state")
	callback := httptest.NewRecorder()
	handler.ServeHTTP(callback, httptest.NewRequest(http.MethodGet, "/oauth/github/callback?code=octocat&state="+url.QueryEscape(state), nil))
	if callback.Code != http.StatusFound {
		t.Fatalf("callback = %d %q", callback.Code, callback.Body.String())
	}
	if len(callback.Result().Cookies()) != 1 {
		t.Fatalf("cookies = %#v", callback.Result().Cookies())
	}
	onboardingRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	onboardingRequest.AddCookie(callback.Result().Cookies()[0])
	onboarding := httptest.NewRecorder()
	handler.ServeHTTP(onboarding, onboardingRequest)
	if !strings.Contains(onboarding.Body.String(), "Sandcastle Onboarding") || !strings.Contains(onboarding.Body.String(), "sandcastle login https://auth.example.test") {
		t.Fatalf("onboarding = %d %q", onboarding.Code, onboarding.Body.String())
	}

	client := authapp.DeviceClient{BaseURL: server.URL, HTTPClient: server.Client()}
	started, err := client.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	approveRequest := httptest.NewRequest(http.MethodPost, "/device", strings.NewReader("user_code="+started.UserCode+"&action=approve"))
	approveRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	approveRequest.AddCookie(callback.Result().Cookies()[0])
	approve := httptest.NewRecorder()
	handler.ServeHTTP(approve, approveRequest)
	if approve.Code != http.StatusOK || !strings.Contains(approve.Body.String(), "Return to the terminal") {
		t.Fatalf("approve = %d %q", approve.Code, approve.Body.String())
	}

	publicKey := firstRunAuthorizedKey(t)
	polled, err := client.Poll(ctx, started.DeviceCode, authapp.DevicePollRequest{SSHPublicKey: publicKey})
	if err != nil {
		t.Fatal(err)
	}
	if polled.Status != authapp.DeviceStatusApproved || polled.LoginResult == nil {
		t.Fatalf("poll = %#v", polled)
	}
	if polled.LoginResult.SelectedUser != "octocat" ||
		polled.LoginResult.CurrentTenant != "octocat" ||
		polled.LoginResult.CurrentProject != "default" ||
		polled.LoginResult.CredentialEnrollment.IncusCertificateAddToken != "token-octocat" ||
		polled.LoginResult.SSHKeyFingerprint == "" ||
		polled.LoginResult.NextCommand != "sandcastle create dev" {
		t.Fatalf("login result = %#v", polled.LoginResult)
	}
	if len(reconciler.calls) != 1 || reconciler.calls[0].key != publicKey {
		t.Fatalf("reconciler calls = %#v", reconciler.calls)
	}
	stored, err := authapp.GetUserSSHKey(ctx, db, "octocat")
	if err != nil {
		t.Fatal(err)
	}
	if stored.PublicKey != publicKey {
		t.Fatalf("stored key = %#v", stored)
	}

	admin := testAdminForFirstRun()
	createPlan, err := machine.PlanCreate(ctx, admin, tenantStore, firstRunMachineStore{}, machine.CreateRequest{Reference: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if createPlan.Project != "default" ||
		createPlan.Name != "dev" ||
		createPlan.InstanceName != "default-dev" ||
		createPlan.Hostname != "dev.default.octocat" ||
		createPlan.PrivateIP == "" ||
		!createPlan.StartsByDefault {
		t.Fatalf("create plan = %#v", createPlan)
	}

	connectPlan, err := machine.PlanConnect(ctx, admin, tenantStore, firstRunMachineStore{machines: []meta.Machine{{
		Tenant:      "octocat",
		Project:     "default",
		Name:        "dev",
		PrivateIP:   "10.248.1.20",
		TailscaleIP: "100.64.1.20",
	}}}, machine.ConnectRequest{Reference: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if connectPlan.SSHHost != "10.248.1.20" || connectPlan.HostKeyAlias != "dev.default.octocat" {
		t.Fatalf("connect plan = %#v", connectPlan)
	}
}

type firstRunProvisioner struct{}

func (p *firstRunProvisioner) EnsurePersonalTenant(ctx context.Context, user authapp.User) (authapp.PersonalTenantResult, error) {
	return authapp.PersonalTenantResult{
		UserKey:             user.UserKey,
		Tenant:              user.UserKey,
		IncusProject:        "sc-" + user.UserKey,
		AccessibleTenants:   []string{user.UserKey},
		Token:               "token-" + user.UserKey,
		RemoteName:          "sandcastle-" + user.UserKey,
		Projects:            []string{"sc-" + user.UserKey},
		CurrentProject:      "default",
		DefaultProjectReady: true,
		TenantTailnetReady:  true,
		Message:             "Personal tenant " + user.UserKey + " is ready.",
	}, nil
}

type firstRunSSHReconciler struct {
	calls []struct {
		tenant string
		user   string
		key    string
	}
}

func (r *firstRunSSHReconciler) ReconcileUserSSHKey(ctx context.Context, summary tenantpkg.Summary, userKey string, publicKey string) error {
	r.calls = append(r.calls, struct {
		tenant string
		user   string
		key    string
	}{tenant: summary.Tenant, user: userKey, key: publicKey})
	return nil
}

type firstRunMachineStore struct {
	machines []meta.Machine
}

func (s firstRunMachineStore) ListMachines(ctx context.Context, summary tenantpkg.Summary) ([]meta.Machine, error) {
	return append([]meta.Machine{}, s.machines...), nil
}

func testAdminForFirstRun() config.Admin {
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "octocat"
	return admin
}

func firstRunAuthorizedKey(t *testing.T) string {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPublicKey, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublicKey)))
}
