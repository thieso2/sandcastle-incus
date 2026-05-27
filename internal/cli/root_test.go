package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/authapp"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	"github.com/thieso2/sandcastle-incus/internal/domain"
	"github.com/thieso2/sandcastle-incus/internal/hostoverride"
	"github.com/thieso2/sandcastle-incus/internal/images"
	"github.com/thieso2/sandcastle-incus/internal/infra"
	"github.com/thieso2/sandcastle-incus/internal/localdns"
	"github.com/thieso2/sandcastle-incus/internal/localtrust"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/route"
	"github.com/thieso2/sandcastle-incus/internal/routebroker"
	"github.com/thieso2/sandcastle-incus/internal/tailscale"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

func executeForTest(t *testing.T, name string, args ...string) (string, error) {
	return executeForTestWithConfig(t, commandConfig{name: name}, args...)
}

func envContains(env []string, value string) bool {
	for _, entry := range env {
		if entry == value {
			return true
		}
	}
	return false
}

func executeForTestWithConfig(t *testing.T, config commandConfig, args ...string) (string, error) {
	t.Helper()
	stdout, stderr, err := executeForTestWithConfigAndStderr(t, config, args...)
	if stderr != "" {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
	return stdout, err
}

func executeForTestWithConfigAndStderr(t *testing.T, config commandConfig, args ...string) (string, string, error) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	config.stdout = &stdout
	config.stderr = &stderr
	if config.adminConfig.Remote == "" {
		config.adminConfig = testAdminConfig()
	}
	cmd := NewRootCommand(config)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func executeAdminForTest(t *testing.T, name string, args ...string) (string, error) {
	return executeAdminForTestWithConfig(t, commandConfig{name: name}, args...)
}

func executeAdminForTestWithConfig(t *testing.T, config commandConfig, args ...string) (string, error) {
	t.Helper()
	stdout, stderr, err := executeAdminForTestWithConfigAndStderr(t, config, args...)
	if stderr != "" {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
	return stdout, err
}

func executeAdminForTestWithConfigAndStderr(t *testing.T, config commandConfig, args ...string) (string, string, error) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	config.stdout = &stdout
	config.stderr = &stderr
	if config.adminConfig.Remote == "" {
		config.adminConfig = testAdminConfig()
	}
	cmd := NewAdminRootCommand(config)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func testAdminConfig() scconfig.Admin {
	admin := scconfig.LoadAdminFromEnv()
	if admin.Tenant == "" {
		admin.Tenant = "acme"
	}
	return admin
}

type fakeAuthDeviceClient struct {
	start            authapp.DeviceStartResult
	polls            []authapp.DevicePollResult
	polledDeviceCode string
	pollRequests     []authapp.DevicePollRequest
}

type fakeAuthWorkloadClient struct {
	start          authapp.DeviceStartResult
	starts         int
	polls          []authapp.DevicePollResult
	enableRequests []authapp.WorkloadEnableRequest
	enableResult   authapp.WorkloadEnableResult
}

type fakeAuthCloudIdentityClient struct {
	upsertRequests []authapp.CloudIdentityUpsertRequest
	upsertResult   authapp.CloudIdentityConfig
}

type fakeLoginRemoteInstaller struct {
	requests []loginRemoteInstallRequest
}

type fakeLoginTailnetVerifier struct {
	requests []string
	status   loginTailnetStatus
	err      error
}

type fakeLoginSetupRunner struct {
	requests []loginSetupRequest
	result   loginSetupResult
}

type fakeGCloudRunner struct {
	calls         [][]string
	projectNumber string
	existing      map[string]bool
}

func (r *fakeGCloudRunner) run(_ context.Context, args []string, _ io.Writer) (string, error) {
	copied := append([]string{}, args...)
	r.calls = append(r.calls, copied)
	if r.projectNumber == "" {
		r.projectNumber = "123456789012"
	}
	if len(args) == 3 && args[0] == "config" && args[1] == "get-value" && args[2] == "project" {
		return "example-gcp", nil
	}
	if len(args) == 4 && args[0] == "projects" && args[1] == "describe" && args[3] == "--format=value(projectNumber)" {
		return r.projectNumber, nil
	}
	if containsArg(args, "describe") {
		if r.existing != nil && r.existing[strings.Join(args, "\x00")] {
			return "", nil
		}
		return "", fmt.Errorf("not found")
	}
	return "", nil
}

func (r *fakeGCloudRunner) hasCall(want ...string) bool {
	for _, call := range r.calls {
		if len(call) != len(want) {
			continue
		}
		matches := true
		for i := range call {
			if call[i] != want[i] {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func (r *fakeGCloudRunner) hasCallContaining(fragment string) bool {
	for _, call := range r.calls {
		if strings.Contains(strings.Join(call, " "), fragment) {
			return true
		}
	}
	return false
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func (i *fakeLoginRemoteInstaller) InstallLoginRemote(ctx context.Context, request loginRemoteInstallRequest) (loginRemoteInstallResult, error) {
	i.requests = append(i.requests, request)
	return loginRemoteInstallResult{RemoteName: request.RemoteName, Tenant: request.Tenant, IncusConfig: "/tmp/incus"}, nil
}

func (v *fakeLoginTailnetVerifier) VerifyTenantTailnet(ctx context.Context, tailnet string) (loginTailnetStatus, error) {
	v.requests = append(v.requests, tailnet)
	if v.err != nil {
		return loginTailnetStatus{}, v.err
	}
	return v.status, nil
}

func (r *fakeLoginSetupRunner) RunPostLoginSetup(ctx context.Context, request loginSetupRequest) (loginSetupResult, error) {
	r.requests = append(r.requests, request)
	if r.result.DNS.Reference == "" {
		r.result.DNS = dnsSetupResult{
			Reference: request.Tenant,
			Apply:     dns.ApplyResult{RecordCount: 2},
			Install:   localdns.Result{StatePath: "/tmp/dns.yaml", ResolverPath: "/tmp/resolver"},
		}
	}
	if r.result.Trust.Reference == "" {
		r.result.Trust = localtrust.Result{
			Reference: request.Tenant,
			Action:    "install",
			Target:    "/tmp/trust",
		}
	}
	if r.result.Tailscale.Reference == "" {
		r.result.Tailscale = tailscale.UpPlan{
			Reference:       request.Tenant,
			InstanceName:    "sc-" + request.Tenant,
			AdvertiseRoutes: []string{"10.248.0.0/24"},
			HasAuthKey:      request.TailscaleAuthKey != "",
		}
	}
	return r.result, nil
}

func (c *fakeAuthDeviceClient) Start(ctx context.Context) (authapp.DeviceStartResult, error) {
	return c.start, nil
}

func (c *fakeAuthDeviceClient) Poll(ctx context.Context, deviceCode string, request authapp.DevicePollRequest) (authapp.DevicePollResult, error) {
	c.polledDeviceCode = deviceCode
	c.pollRequests = append(c.pollRequests, request)
	if len(c.polls) == 0 {
		return authapp.DevicePollResult{Status: authapp.DeviceStatusExpired}, nil
	}
	next := c.polls[0]
	c.polls = c.polls[1:]
	return next, nil
}

func (c *fakeAuthDeviceClient) DebugApprove(ctx context.Context, userCode string) error {
	return nil
}

func (c *fakeAuthWorkloadClient) Start(ctx context.Context) (authapp.DeviceStartResult, error) {
	c.starts++
	if c.start.DeviceCode == "" {
		c.start = authapp.DeviceStartResult{DeviceCode: "device", UserCode: "ABCD-1234", VerificationURI: "https://auth.example.com/device", Interval: 1}
	}
	return c.start, nil
}

func (c *fakeAuthWorkloadClient) Poll(ctx context.Context, deviceCode string, request authapp.DevicePollRequest) (authapp.DevicePollResult, error) {
	if len(c.polls) == 0 {
		return authapp.DevicePollResult{Status: authapp.DeviceStatusApproved, UserKey: "acme"}, nil
	}
	result := c.polls[0]
	c.polls = c.polls[1:]
	return result, nil
}

func (c *fakeAuthWorkloadClient) DebugApprove(ctx context.Context, userCode string) error {
	return nil
}

func (c *fakeAuthWorkloadClient) EnableWorkload(ctx context.Context, request authapp.WorkloadEnableRequest) (authapp.WorkloadEnableResult, error) {
	c.enableRequests = append(c.enableRequests, request)
	if c.enableResult.RuntimeSecret == "" {
		c.enableResult = authapp.WorkloadEnableResult{
			Tenant:                            request.Tenant,
			Project:                           request.Project,
			Machine:                           request.Machine,
			RuntimeSecret:                     "runtime-secret",
			TokenEndpoint:                     "https://auth.example.com/internal/workload/token",
			Issuer:                            "https://auth.example.com/t/" + request.Tenant,
			CloudIdentityConfig:               request.CloudIdentityConfig,
			GCPAudience:                       "//iam.googleapis.com/projects/123/locations/global/workloadIdentityPools/sandcastle-" + request.Tenant + "/providers/sandcastle",
			GCPServiceAccountImpersonationURL: "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/sa@example.iam.gserviceaccount.com:generateAccessToken",
		}
	}
	return c.enableResult, nil
}

func (c *fakeAuthCloudIdentityClient) UpsertCloudIdentity(ctx context.Context, request authapp.CloudIdentityUpsertRequest) (authapp.CloudIdentityConfig, error) {
	c.upsertRequests = append(c.upsertRequests, request)
	if c.upsertResult.Name == "" {
		c.upsertResult = authapp.CloudIdentityConfig{
			Name:                              request.Name,
			Provider:                          request.Provider,
			GCPAudience:                       request.GCPAudience,
			GCPSubjectTokenType:               request.GCPSubjectTokenType,
			GCPServiceAccountImpersonationURL: request.GCPServiceAccountImpersonationURL,
		}
	}
	return c.upsertResult, nil
}

func TestVersionText(t *testing.T) {
	stdout, err := executeForTest(t, "sandcastle", "version")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(stdout); got != version {
		t.Fatalf("version output = %q, want %q", got, version)
	}
}

func TestVersionJSONUsesBinaryName(t *testing.T) {
	stdout, err := executeForTest(t, "sc", "--output", "json", "version")
	if err != nil {
		t.Fatal(err)
	}
	var payload versionPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Name != "sc" {
		t.Fatalf("payload.Name = %q, want sc", payload.Name)
	}
	if payload.Version != version {
		t.Fatalf("payload.Version = %q, want %q", payload.Version, version)
	}
}

func TestJSONFlagUsesJSONOutput(t *testing.T) {
	stdout, err := executeForTest(t, "sandcastle", "--json", "version")
	if err != nil {
		t.Fatal(err)
	}
	var payload versionPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Name != "sandcastle" {
		t.Fatalf("payload.Name = %q, want sandcastle", payload.Name)
	}
}

func TestJSONFlagRejectsExplicitTextOutput(t *testing.T) {
	_, err := executeForTest(t, "sandcastle", "--json", "--output", "text", "version")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--json") {
		t.Fatalf("error = %q", err)
	}
}

func TestLoginStartsDeviceFlowAndReportsApproval(t *testing.T) {
	useLoginHomeForTest(t)
	installer := &fakeLoginRemoteInstaller{}
	client := &fakeAuthDeviceClient{
		start: authapp.DeviceStartResult{
			DeviceCode:      "device",
			UserCode:        "ABCD-1234",
			VerificationURI: "https://auth.example.com/device?user_code=ABCD-1234",
			Interval:        1,
			Message:         "Waiting for browser approval.",
		},
		polls: []authapp.DevicePollResult{{
			Status:            authapp.DeviceStatusApproved,
			Message:           "Personal tenant octocat is ready.",
			UserKey:           "octocat",
			CLIAuthToken:      "cli-token",
			Token:             "token",
			RemoteName:        "sandcastle-octocat",
			AccessibleTenants: []string{"octocat"},
		}},
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		authDevice:  client,
		loginRemote: installer,
	}, "login", "https://auth.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"SSH key: SHA256:",
		"Open: https://auth.example.com/device?user_code=ABCD-1234",
		"Code: ABCD-1234",
		"Approved as octocat.",
		"Remote \"sandcastle-octocat\" enrolled.",
		"Default tenant set to \"octocat\".",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if client.polledDeviceCode != "device" {
		t.Fatalf("polled device code = %q", client.polledDeviceCode)
	}
	if len(client.pollRequests) != 1 || !strings.HasPrefix(client.pollRequests[0].SSHPublicKey, "ssh-ed25519 ") {
		t.Fatalf("poll requests = %#v", client.pollRequests)
	}
	if len(installer.requests) != 1 || installer.requests[0].Token != "token" || installer.requests[0].Tenant != "octocat" {
		t.Fatalf("installer requests = %#v", installer.requests)
	}
	cfg, err := scconfig.LoadSandcastleConfig(scconfig.DefaultConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthHostname != "https://auth.example.com" {
		t.Fatalf("AuthHostname = %q", cfg.AuthHostname)
	}
	if cfg.AuthToken != "cli-token" {
		t.Fatalf("AuthToken = %q", cfg.AuthToken)
	}
}

func TestLoginDoesNotRepeatUnchangedDeviceMessage(t *testing.T) {
	useLoginHomeForTest(t)
	installer := &fakeLoginRemoteInstaller{}
	client := &fakeAuthDeviceClient{
		start: authapp.DeviceStartResult{
			DeviceCode:      "device",
			UserCode:        "ABCD-1234",
			VerificationURI: "https://auth.example.com/device?user_code=ABCD-1234",
			Interval:        1,
			Message:         "Waiting for browser approval.",
		},
		polls: []authapp.DevicePollResult{{
			Status:            authapp.DeviceStatusApproved,
			Message:           "Waiting for browser approval.",
			UserKey:           "octocat",
			Token:             "token",
			RemoteName:        "sandcastle-octocat",
			AccessibleTenants: []string{"octocat"},
		}},
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		authDevice:  client,
		loginRemote: installer,
	}, "login", "https://auth.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(stdout, "Waiting for browser approval.") != 1 {
		t.Fatalf("stdout repeated device message:\n%s", stdout)
	}
}

func TestLoginVerboseReportsPollResult(t *testing.T) {
	useLoginHomeForTest(t)
	t.Setenv("VERBOSE", "1")
	installer := &fakeLoginRemoteInstaller{}
	client := &fakeAuthDeviceClient{
		start: authapp.DeviceStartResult{
			DeviceCode:      "device",
			UserCode:        "ABCD-1234",
			VerificationURI: "https://auth.example.com/device?user_code=ABCD-1234",
			Interval:        1,
			ExpiresIn:       600,
		},
		polls: []authapp.DevicePollResult{{
			Status:            authapp.DeviceStatusApproved,
			Message:           "Personal tenant octocat is ready.",
			UserKey:           "octocat",
			Token:             "token",
			RemoteName:        "sandcastle-octocat",
			AccessibleTenants: []string{"octocat"},
			ExpiresIn:         590,
		}},
	}
	_, stderr, err := executeForTestWithConfigAndStderr(t, commandConfig{
		name:        "sandcastle",
		authDevice:  client,
		loginRemote: installer,
	}, "login", "https://auth.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[verbose] login: auth host=https://auth.example.com",
		"[verbose] login: device start: interval=1s expires_in=600s",
		"[verbose] login: poll result: status=approved expires_in=590s user=octocat remote=sandcastle-octocat tenants=octocat",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "poll attempt=") {
		t.Fatalf("stderr should not contain poll attempt lines:\n%s", stderr)
	}
	if strings.Contains(stderr, "token") {
		t.Fatalf("stderr leaked token:\n%s", stderr)
	}
}

func TestLoginRunsPostSetupForSingleTenant(t *testing.T) {
	installer := &fakeLoginRemoteInstaller{}
	setup := &fakeLoginSetupRunner{}
	client := &fakeAuthDeviceClient{
		start: authapp.DeviceStartResult{DeviceCode: "device", UserCode: "ABCD-1234", VerificationURI: "https://auth.example.com/device", Interval: 1},
		polls: []authapp.DevicePollResult{{
			Status:            authapp.DeviceStatusApproved,
			UserKey:           "octocat",
			Token:             "token",
			RemoteName:        "sandcastle-octocat",
			AccessibleTenants: []string{"octocat"},
		}},
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		authDevice:  client,
		loginRemote: installer,
		loginSetup:  setup,
	}, "login", "https://auth.example.com", "--tailscale-auth-key", "tskey-secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(setup.requests) != 1 {
		t.Fatalf("setup requests = %#v", setup.requests)
	}
	request := setup.requests[0]
	if request.RemoteName != "sandcastle-octocat" || request.IncusConfig != "/tmp/incus" || request.Tenant != "octocat" || request.TailscaleAuthKey != "tskey-secret" {
		t.Fatalf("setup request = %#v", request)
	}
	for _, want := range []string{
		"Setting up DNS, trust, and Tailscale for \"octocat\".",
		"DNS setup: octocat",
		"install tenant CA trust: octocat",
		"Tailscale: octocat",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "tskey-secret") {
		t.Fatalf("stdout leaked auth key: %s", stdout)
	}
}

func TestLoginUsesE2ETailscaleAuthKeyFallback(t *testing.T) {
	t.Setenv("SANDCASTLE_E2E_TAILSCALE_AUTHKEY", "tskey-e2e")
	installer := &fakeLoginRemoteInstaller{}
	setup := &fakeLoginSetupRunner{}
	client := &fakeAuthDeviceClient{
		start: authapp.DeviceStartResult{DeviceCode: "device", UserCode: "ABCD-1234", VerificationURI: "https://auth.example.com/device", Interval: 1},
		polls: []authapp.DevicePollResult{{
			Status:            authapp.DeviceStatusApproved,
			UserKey:           "octocat",
			Token:             "token",
			RemoteName:        "sandcastle-octocat",
			AccessibleTenants: []string{"octocat"},
		}},
	}
	_, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		authDevice:  client,
		loginRemote: installer,
		loginSetup:  setup,
	}, "login", "https://auth.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(setup.requests) != 1 || setup.requests[0].TailscaleAuthKey != "tskey-e2e" {
		t.Fatalf("setup requests = %#v", setup.requests)
	}
}

func TestLoginUsesAuthAppTailscaleAuthKeyBeforeEnvFallback(t *testing.T) {
	t.Setenv("SANDCASTLE_E2E_TAILSCALE_AUTHKEY", "tskey-e2e")
	installer := &fakeLoginRemoteInstaller{}
	setup := &fakeLoginSetupRunner{}
	client := &fakeAuthDeviceClient{
		start: authapp.DeviceStartResult{DeviceCode: "device", UserCode: "ABCD-1234", VerificationURI: "https://auth.example.com/device", Interval: 1},
		polls: []authapp.DevicePollResult{{
			Status:            authapp.DeviceStatusApproved,
			UserKey:           "octocat",
			Token:             "token",
			RemoteName:        "sandcastle-octocat",
			AccessibleTenants: []string{"octocat"},
			TailscaleAuthKey:  "tskey-server",
		}},
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		authDevice:  client,
		loginRemote: installer,
		loginSetup:  setup,
	}, "login", "https://auth.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(setup.requests) != 1 || setup.requests[0].TailscaleAuthKey != "tskey-server" {
		t.Fatalf("setup requests = %#v", setup.requests)
	}
	if strings.Contains(stdout, "tskey-server") {
		t.Fatalf("stdout leaked auth key: %s", stdout)
	}
}

func TestLoginSetupIncusConfigPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "incus")
	file := filepath.Join(dir, "config.yml")
	if got := loginSetupIncusDir(dir); got != dir {
		t.Fatalf("loginSetupIncusDir(dir) = %q, want %q", got, dir)
	}
	if got := loginSetupIncusConfigFile(dir); got != file {
		t.Fatalf("loginSetupIncusConfigFile(dir) = %q, want %q", got, file)
	}
	if got := loginSetupIncusDir(file); got != dir {
		t.Fatalf("loginSetupIncusDir(file) = %q, want %q", got, dir)
	}
	if got := loginSetupIncusConfigFile(file); got != file {
		t.Fatalf("loginSetupIncusConfigFile(file) = %q, want %q", got, file)
	}
}

func TestLoginSkipSetupDoesNotRunPostSetup(t *testing.T) {
	installer := &fakeLoginRemoteInstaller{}
	setup := &fakeLoginSetupRunner{}
	client := &fakeAuthDeviceClient{
		start: authapp.DeviceStartResult{DeviceCode: "device", UserCode: "ABCD-1234", VerificationURI: "https://auth.example.com/device", Interval: 1},
		polls: []authapp.DevicePollResult{{
			Status:            authapp.DeviceStatusApproved,
			UserKey:           "octocat",
			Token:             "token",
			RemoteName:        "sandcastle-octocat",
			AccessibleTenants: []string{"octocat"},
		}},
	}
	_, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		authDevice:  client,
		loginRemote: installer,
		loginSetup:  setup,
	}, "login", "https://auth.example.com", "--skip-setup")
	if err != nil {
		t.Fatal(err)
	}
	if len(setup.requests) != 0 {
		t.Fatalf("setup requests = %#v", setup.requests)
	}
}

func TestLoginDoesNotSetTenantWhenNoAccessibleTenants(t *testing.T) {
	useLoginHomeForTest(t)
	installer := &fakeLoginRemoteInstaller{}
	client := &fakeAuthDeviceClient{
		start: authapp.DeviceStartResult{DeviceCode: "device", UserCode: "ABCD-1234", VerificationURI: "https://auth.example.com/device", Interval: 1},
		polls: []authapp.DevicePollResult{{
			Status:     authapp.DeviceStatusApproved,
			UserKey:    "octocat",
			Token:      "token",
			RemoteName: "sandcastle-octocat",
		}},
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		authDevice:  client,
		loginRemote: installer,
	}, "login", "https://auth.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(installer.requests) != 1 || installer.requests[0].Tenant != "" {
		t.Fatalf("installer requests = %#v", installer.requests)
	}
	if !strings.Contains(stdout, "No default tenant set; no accessible tenants were returned.") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestLoginVerifiesTenantTailnetFromLoginResult(t *testing.T) {
	useLoginHomeForTest(t)
	installer := &fakeLoginRemoteInstaller{}
	verifier := &fakeLoginTailnetVerifier{status: loginTailnetStatus{Tailnet: "tailnet.example", IPs: []string{"100.64.0.10"}}}
	client := &fakeAuthDeviceClient{
		start: authapp.DeviceStartResult{DeviceCode: "device", UserCode: "ABCD-1234", VerificationURI: "https://auth.example.com/device", Interval: 1},
		polls: []authapp.DevicePollResult{{
			Status:            authapp.DeviceStatusApproved,
			UserKey:           "octocat",
			Token:             "token",
			RemoteName:        "sandcastle-octocat",
			AccessibleTenants: []string{"octocat"},
			LoginResult: &authapp.CLILoginResult{
				TenantTailnetStatus: authapp.TenantTailnetStatus{Tailnet: "tailnet.example"},
			},
		}},
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:         "sandcastle",
		authDevice:   client,
		loginRemote:  installer,
		loginTailnet: verifier,
	}, "login", "https://auth.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier.requests) != 1 || verifier.requests[0] != "tailnet.example" {
		t.Fatalf("verifier requests = %#v", verifier.requests)
	}
	for _, want := range []string{
		"Join Tenant Tailnet \"tailnet.example\"",
		"Tenant Tailnet \"tailnet.example\" connected with IP 100.64.0.10.",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestLoginReportsDeniedDeviceFlow(t *testing.T) {
	useLoginHomeForTest(t)
	client := &fakeAuthDeviceClient{
		start: authapp.DeviceStartResult{DeviceCode: "device", UserCode: "ABCD-1234", VerificationURI: "https://auth.example.com/device", Interval: 1},
		polls: []authapp.DevicePollResult{{
			Status: authapp.DeviceStatusDenied,
		}},
	}
	_, err := executeForTestWithConfig(t, commandConfig{
		name:       "sandcastle",
		authDevice: client,
	}, "login", "https://auth.example.com")
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoginAcceptsExplicitSSHPublicKeyPath(t *testing.T) {
	useLoginHomeForTest(t)
	keyPath := filepath.Join(t.TempDir(), "login.pub")
	if err := os.WriteFile(keyPath, []byte(validAuthorizedKeyForTest(t)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &fakeAuthDeviceClient{
		start: authapp.DeviceStartResult{DeviceCode: "device", UserCode: "ABCD-1234", VerificationURI: "https://auth.example.com/device", Interval: 1},
		polls: []authapp.DevicePollResult{{
			Status: authapp.DeviceStatusApproved,
		}},
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:       "sandcastle",
		authDevice: client,
	}, "login", "--ssh-public-key", keyPath, "https://auth.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "SSH key: SHA256:") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestLoginRejectsInvalidExplicitSSHPublicKeyPath(t *testing.T) {
	useLoginHomeForTest(t)
	keyPath := filepath.Join(t.TempDir(), "login.pub")
	if err := os.WriteFile(keyPath, []byte("ssh-ed25519 not-base64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &fakeAuthDeviceClient{
		start: authapp.DeviceStartResult{DeviceCode: "device", UserCode: "ABCD-1234", VerificationURI: "https://auth.example.com/device", Interval: 1},
	}
	_, err := executeForTestWithConfig(t, commandConfig{
		name:       "sandcastle",
		authDevice: client,
	}, "login", "--ssh-public-key", keyPath, "https://auth.example.com")
	if err == nil || !strings.Contains(err.Error(), "parse SSH public key") {
		t.Fatalf("error = %v", err)
	}
	if client.polledDeviceCode != "" {
		t.Fatalf("device flow started after invalid SSH key: %q", client.polledDeviceCode)
	}
}

func TestConfigUnsetClearsStoredValue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configPath := scconfig.DefaultConfigPath()
	if err := scconfig.SaveSandcastleConfig(configPath, scconfig.SandcastleConfig{
		Tenant:      "acme",
		Project:     "website",
		Remote:      "sc-acme",
		AdminRemote: "big",
	}); err != nil {
		t.Fatal(err)
	}

	stdout, err := executeForTest(t, "sandcastle", "config", "unset", "project")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Unset project") {
		t.Fatalf("stdout = %q", stdout)
	}
	cfg, err := scconfig.LoadSandcastleConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project != "" {
		t.Fatalf("Project = %q, want empty", cfg.Project)
	}
	if cfg.Tenant != "acme" || cfg.Remote != "sc-acme" || cfg.AdminRemote != "big" {
		t.Fatalf("config = %#v, want other keys preserved", cfg)
	}
}

func TestConfigSetAuthHostname(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configPath := scconfig.DefaultConfigPath()
	stdout, err := executeForTest(t, "sandcastle", "config", "set", "auth.hostname", "big.example.dev")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Set auth.hostname") {
		t.Fatalf("stdout = %q", stdout)
	}
	cfg, err := scconfig.LoadSandcastleConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthHostname != "big.example.dev" {
		t.Fatalf("AuthHostname = %q", cfg.AuthHostname)
	}
}

func TestConfigUnsetRejectsUnknownKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := executeForTest(t, "sandcastle", "config", "unset", "bad")
	if err == nil || !strings.Contains(err.Error(), "supported keys: tenant, project, remote, auth.hostname, admin_remote") {
		t.Fatalf("err = %v", err)
	}
}

func TestCommandAuthHostnameInfersFromRemoteConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	incusDir := scconfig.RemoteIncusDir("sandcastle-acme")
	if err := os.MkdirAll(incusDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(incusDir, "config.yml"), []byte("remotes:\n  sandcastle-acme:\n    addr: https://big.thieso2.dev:8443\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	admin := testAdminConfig()
	admin.Remote = "sandcastle-acme"
	admin.AuthHostname = ""
	if got := commandAuthHostname(commandConfig{adminConfig: admin}, ""); got != "big.thieso2.dev" {
		t.Fatalf("auth hostname = %q", got)
	}
}

func TestCommandAuthHostnamePrefersCurrentRemoteOverSavedConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	incusDir := scconfig.RemoteIncusDir("sandcastle-thieso2")
	if err := os.MkdirAll(incusDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(incusDir, "config.yml"), []byte("remotes:\n  sandcastle-thieso2:\n    addr: https://big.thieso2.dev:8443\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	admin := testAdminConfig()
	admin.Remote = "sandcastle-thieso2"
	admin.AuthHostname = "https://auth.example.com"
	if got := commandAuthHostname(commandConfig{adminConfig: admin}, ""); got != "big.thieso2.dev" {
		t.Fatalf("auth hostname = %q", got)
	}
}

func TestCommandAuthHostnameExplicitOverridesRemoteInference(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SANDCASTLE_AUTH_HOSTNAME", "env.example.dev")
	incusDir := scconfig.RemoteIncusDir("sandcastle-thieso2")
	if err := os.MkdirAll(incusDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(incusDir, "config.yml"), []byte("remotes:\n  sandcastle-thieso2:\n    addr: https://big.thieso2.dev:8443\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	admin := testAdminConfig()
	admin.Remote = "sandcastle-thieso2"
	admin.AuthHostname = "https://auth.example.com"
	config := commandConfig{adminConfig: admin}
	if got := commandAuthHostname(config, "flag.example.dev"); got != "flag.example.dev" {
		t.Fatalf("flag auth hostname = %q", got)
	}
	if got := commandAuthHostname(config, ""); got != "env.example.dev" {
		t.Fatalf("env auth hostname = %q", got)
	}
}

func TestCloudIdentityGCPSetupConfiguresTenantFederation(t *testing.T) {
	runner := &fakeGCloudRunner{}
	admin := testAdminConfig()
	admin.Tenant = "thieso2"
	admin.AuthHostname = "big.thieso2.dev"
	stdout, stderr, err := executeForTestWithConfigAndStderr(t, commandConfig{
		name:         "sandcastle",
		adminConfig:  admin,
		gcloudRunner: runner.run,
	}, "cloud-identity", "gcp", "setup", "--project", "example-gcp", "--role", "roles/storage.objectAdmin")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Configured Sandcastle GCP Workload Identity Federation.",
		"Issuer URI:               https://big.thieso2.dev/t/thieso2",
		"Cloud Identity Audience:  //iam.googleapis.com/projects/123456789012/locations/global/workloadIdentityPools/sandcastle-thieso2/providers/sandcastle",
		"Impersonation URL:        https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/sandcastle-thieso2@example-gcp.iam.gserviceaccount.com:generateAccessToken",
		"Web UI:                   https://big.thieso2.dev/cloud-identities",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	for _, want := range []string{
		"+ gcloud services enable iam.googleapis.com",
		"+ gcloud iam workload-identity-pools create sandcastle-thieso2",
		"+ gcloud iam workload-identity-pools providers create-oidc sandcastle",
		"+ gcloud iam service-accounts create sandcastle-thieso2",
		"+ gcloud projects add-iam-policy-binding example-gcp",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if !runner.hasCall("iam", "service-accounts", "add-iam-policy-binding", "sandcastle-thieso2@example-gcp.iam.gserviceaccount.com", "--project=example-gcp", "--role=roles/iam.workloadIdentityUser", "--member=principalSet://iam.googleapis.com/projects/123456789012/locations/global/workloadIdentityPools/sandcastle-thieso2/attribute.tenant/thieso2") {
		t.Fatalf("missing tenant-scoped workloadIdentityUser binding: %#v", runner.calls)
	}
	if !runner.hasCallContaining("--attribute-condition=assertion.tenant=='thieso2'") {
		t.Fatalf("missing tenant attribute condition: %#v", runner.calls)
	}
}

func TestCloudIdentityGCPSetupSavesConfigInAuthApp(t *testing.T) {
	runner := &fakeGCloudRunner{}
	authClient := &fakeAuthCloudIdentityClient{}
	admin := testAdminConfig()
	admin.Tenant = "thieso2"
	admin.AuthHostname = "big.thieso2.dev"
	admin.AuthToken = "stored-token"
	stdout, _, err := executeForTestWithConfigAndStderr(t, commandConfig{
		name:              "sandcastle",
		adminConfig:       admin,
		gcloudRunner:      runner.run,
		authCloudIdentity: authClient,
	}, "cloud-identity", "gcp", "setup", "--project", "example-gcp")
	if err != nil {
		t.Fatal(err)
	}
	if len(authClient.upsertRequests) != 1 {
		t.Fatalf("upsert requests = %#v", authClient.upsertRequests)
	}
	request := authClient.upsertRequests[0]
	if request.Name != "gcp" || request.Provider != "gcp" {
		t.Fatalf("upsert request = %#v", request)
	}
	if request.GCPAudience != "//iam.googleapis.com/projects/123456789012/locations/global/workloadIdentityPools/sandcastle-thieso2/providers/sandcastle" {
		t.Fatalf("audience = %q", request.GCPAudience)
	}
	if request.GCPServiceAccountImpersonationURL != "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/sandcastle-thieso2@example-gcp.iam.gserviceaccount.com:generateAccessToken" {
		t.Fatalf("impersonation URL = %q", request.GCPServiceAccountImpersonationURL)
	}
	if !strings.Contains(stdout, "Saved in Auth App:        yes") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestCloudIdentityGCPSetupUsesCurrentRemoteHostWhenSavedAuthHostnameIsStale(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	incusDir := scconfig.RemoteIncusDir("sandcastle-thieso2")
	if err := os.MkdirAll(incusDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(incusDir, "config.yml"), []byte("remotes:\n  sandcastle-thieso2:\n    addr: https://big.thieso2.dev:8443\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeGCloudRunner{}
	admin := testAdminConfig()
	admin.Tenant = "thieso2"
	admin.Remote = "sandcastle-thieso2"
	admin.AuthHostname = "https://auth.example.com"
	stdout, _, err := executeForTestWithConfigAndStderr(t, commandConfig{
		name:         "sandcastle",
		adminConfig:  admin,
		gcloudRunner: runner.run,
	}, "cloud-identity", "gcp", "setup", "--project", "example-gcp")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Issuer URI:               https://big.thieso2.dev/t/thieso2") {
		t.Fatalf("stdout = %q", stdout)
	}
	if strings.Contains(stdout, "https://auth.example.com/t/thieso2") {
		t.Fatalf("stale auth hostname used:\n%s", stdout)
	}
}

func TestCloudIdentityGCPSetupCanRestrictImpersonationToMachine(t *testing.T) {
	runner := &fakeGCloudRunner{}
	admin := testAdminConfig()
	admin.Tenant = "acme"
	admin.AuthHostname = "auth.example.com"
	_, _, err := executeForTestWithConfigAndStderr(t, commandConfig{
		name:         "sandcastle",
		adminConfig:  admin,
		gcloudRunner: runner.run,
	}, "cloud", "gcp", "setup", "--project", "example-gcp", "--machine-project", "website", "--machine", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if !runner.hasCall("iam", "service-accounts", "add-iam-policy-binding", "sandcastle-acme@example-gcp.iam.gserviceaccount.com", "--project=example-gcp", "--role=roles/iam.workloadIdentityUser", "--member=principal://iam.googleapis.com/projects/123456789012/locations/global/workloadIdentityPools/sandcastle-acme/subject/machine:acme/website/codex") {
		t.Fatalf("missing machine-scoped workloadIdentityUser binding: %#v", runner.calls)
	}
}

func TestWorkloadEnableInjectsHelperAndGCPConfig(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	authClient := &fakeAuthWorkloadClient{}
	creator := &fakeMachineCreator{}
	admin := testAdminConfig()
	admin.Tenant = "acme"
	admin.AuthHostname = "auth.example.com"
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: admin,
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore:   fakeMachineStatusStore{machines: []meta.Machine{{Tenant: "acme", Project: "default", Name: "codex", PrivateIP: "10.248.0.20"}}},
		machineCreator: creator,
		authWorkload:   authClient,
	}, "workload", "enable", "codex", "--cloud-identity", "gcp")
	if err != nil {
		t.Fatal(err)
	}
	if len(authClient.enableRequests) != 1 {
		t.Fatalf("enable requests = %#v", authClient.enableRequests)
	}
	if authClient.enableRequests[0].CloudIdentityConfig != "gcp" || authClient.enableRequests[0].Tenant != "acme" || authClient.enableRequests[0].Machine != "codex" {
		t.Fatalf("enable request = %#v", authClient.enableRequests[0])
	}
	if creator.plan.InstanceName != "default-codex" {
		t.Fatalf("instance = %q", creator.plan.InstanceName)
	}
	if creator.plan.CertificateFiles == nil || len(creator.plan.CertificateFiles) != 0 {
		t.Fatalf("certificate files = %#v, want explicit empty slice", creator.plan.CertificateFiles)
	}
	paths := map[string]bool{}
	for _, file := range creator.plan.WorkloadFiles {
		paths[file.Path] = true
	}
	for _, want := range []string{
		machine.WorkloadRuntimeSecretPath,
		machine.WorkloadTokenHelperPath,
		machine.GCPCredentialPath,
		machine.WorkloadGCPAudiencePath,
	} {
		if !paths[want] {
			t.Fatalf("workload files missing %s: %#v", want, creator.plan.WorkloadFiles)
		}
	}
	if !strings.Contains(stdout, "Helper:         "+machine.WorkloadTokenHelperPath) {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestWorkloadEnableUsesStoredAuthToken(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	authClient := &fakeAuthWorkloadClient{}
	creator := &fakeMachineCreator{}
	admin := testAdminConfig()
	admin.Tenant = "acme"
	admin.AuthHostname = "auth.example.com"
	admin.AuthToken = "stored-token"
	_, err = executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: admin,
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore:   fakeMachineStatusStore{machines: []meta.Machine{{Tenant: "acme", Project: "default", Name: "codex", PrivateIP: "10.248.0.20"}}},
		machineCreator: creator,
		authWorkload:   authClient,
	}, "workload", "enable", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if authClient.starts != 0 {
		t.Fatalf("device flow started %d times", authClient.starts)
	}
	if len(authClient.enableRequests) != 1 || authClient.enableRequests[0].DeviceCode != "" {
		t.Fatalf("enable requests = %#v", authClient.enableRequests)
	}
}

func TestListJSONStartsEmpty(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{},
	}, "--output", "json", "list")
	if err != nil {
		t.Fatal(err)
	}
	var payload listPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Machines) != 0 {
		t.Fatalf("len(payload.Machines) = %d, want 0", len(payload.Machines))
	}
}

func TestListTextShowsManagedMachines(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{{
			Tenant:    "acme",
			Project:   "default",
			Name:      "codex",
			PrivateIP: "10.248.0.20",
			AppPort:   3000,
			Running:   true,
		}}},
	}, "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "default") || !strings.Contains(stdout, "codex") {
		t.Fatalf("stdout = %q, want machine project and name", stdout)
	}
	if strings.Contains(stdout, "Unmanaged:") {
		t.Fatalf("stdout = %q, want no unmanaged footer", stdout)
	}
}

func TestListUsesProjectFromEnv(t *testing.T) {
	t.Setenv("SANDCASTLE_PROJECT", "website")
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{{
			Tenant: "acme", Project: "default", Name: "builder", PrivateIP: "10.248.0.20", AppPort: 3000,
		}, {
			Tenant: "acme", Project: "website", Name: "codex", PrivateIP: "10.248.0.21", AppPort: 3000,
		}}},
	}, "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "website") || !strings.Contains(stdout, "codex") {
		t.Fatalf("stdout = %q, want website/codex", stdout)
	}
	if strings.Contains(stdout, "builder") {
		t.Fatalf("stdout = %q, want env project filter to hide default/builder", stdout)
	}
}

func TestListAliasShowsUnmanagedTenantWide(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{unmanaged: []machine.UnmanagedMachine{{
			Tenant: "acme", Name: "manual", InstanceName: "manual", Status: "Running", Running: true,
		}}},
	}, "ls")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "Unmanaged:") {
		t.Fatalf("stdout = %q, want no unmanaged footer", stdout)
	}
	if !strings.Contains(stdout, "manual") || !strings.Contains(stdout, "unmanaged:Running") {
		t.Fatalf("stdout = %q, want unmanaged row", stdout)
	}
}

func TestListRejectsRemovedUnmanagedFlag(t *testing.T) {
	_, err := executeForTest(t, "sandcastle", "list", "-u")
	if err == nil {
		t.Fatal("expected removed -u flag to be rejected")
	}
	if !strings.Contains(err.Error(), "unknown shorthand flag") {
		t.Fatalf("error = %q", err)
	}
}

func TestListProjectScopeAlsoShowsUnmanagedRows(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{unmanaged: []machine.UnmanagedMachine{{
			Tenant: "acme", Name: "manual", InstanceName: "manual", Status: "Running", Running: true,
		}}},
	}, "list", "default")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "Unmanaged:") {
		t.Fatalf("stdout = %q, want no unmanaged footer", stdout)
	}
	if !strings.Contains(stdout, "manual") || !strings.Contains(stdout, "unmanaged:Running") {
		t.Fatalf("stdout = %q, want unmanaged row", stdout)
	}
}

func TestProjectListShowsCurrentTenantProjects(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "project", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "default") || !strings.Contains(stdout, "website") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestProjectStatusShowsMachineCount(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{
			{Tenant: "acme", Project: "website", Name: "codex"},
			{Tenant: "acme", Project: "default", Name: "shell"},
		}},
	}, "project", "status", "website")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Project: website") || !strings.Contains(stdout, "Machines: 1") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestProjectStatusJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{
			{Tenant: "acme", Project: "website", Name: "codex"},
		}},
	}, "--output", "json", "project", "status", "website")
	if err != nil {
		t.Fatal(err)
	}
	var payload projectStatusPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Tenant.Tenant != "acme" || payload.Project.Name != "website" || payload.MachineCount != 1 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestProjectCreateDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "project", "create", "website", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload tenant.ProjectMutationPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Action != "create" || payload.Project.Name != "website" || len(payload.Projects) != 2 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestProjectCreateCallsUpdater(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	updater := &fakeProjectUpdater{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		tenantUpdater: updater,
	}, "project", "create", "website")
	if err != nil {
		t.Fatal(err)
	}
	if !updater.called || updater.incusProject != "sc-acme" || len(updater.projects) != 2 {
		t.Fatalf("updater = %#v", updater)
	}
}

func TestProjectDeleteRejectsNonEmptyProject(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{{Tenant: "acme", Project: "website", Name: "codex"}}},
	}, "project", "delete", "website", "--yes")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "still contains machine") {
		t.Fatalf("error = %q", err)
	}
}

func TestProjectDeleteCallsUpdater(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	updater := &fakeProjectUpdater{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore:  fakeMachineStatusStore{},
		tenantUpdater: updater,
	}, "project", "delete", "website", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !updater.called || len(updater.projects) != 1 || updater.projects[0].Name != "default" {
		t.Fatalf("updater = %#v", updater)
	}
}

func TestSSHKeySetDryRunUsesCurrentTenant(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "ssh-key", "set", "ssh-ed25519 test", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload sshKeySetPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Tenant != "acme" || payload.IncusProject != "sc-acme" || payload.Key != "ssh-ed25519 test" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestSSHKeySetCallsUpdaterWithFile(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "id_ed25519.pub")
	if err := os.WriteFile(keyFile, []byte("ssh-ed25519 test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	updater := &fakeSSHKeyUpdater{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		tenantSSHKeyUpdater: updater,
	}, "ssh-key", "set", "--file", keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if !updater.called || updater.incusProject != "sc-acme" || updater.key != "ssh-ed25519 test" {
		t.Fatalf("updater = %#v", updater)
	}
}

func TestStatusJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "status", "acme")
	if err != nil {
		t.Fatal(err)
	}
	var payload tenant.Status
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Summary.IncusName != "sc-acme" {
		t.Fatalf("IncusName = %q", payload.Summary.IncusName)
	}
}

func TestStatusJSONUsesTenantRef(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "status", "acme")
	if err != nil {
		t.Fatal(err)
	}
	var payload tenant.Status
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Summary.Tenant != "acme" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestMachineStatusJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{{
			Tenant:         "acme",
			Project:        "default",
			Name:           "codex",
			AppPort:        5173,
			PrivateIP:      "10.248.0.20",
			LinuxUser:      "alice",
			HomeDir:        ".",
			WorkspaceDir:   "workspace",
			ContainerTools: true,
			Running:        true,
		}}},
	}, "--output", "json", "status", "codex")
	if err != nil {
		t.Fatal(err)
	}
	var payload machine.StatusResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.InstanceName != "default-codex" {
		t.Fatalf("InstanceName = %q", payload.InstanceName)
	}
	if payload.Machine.AppPort != 5173 || payload.Machine.LinuxUser != "alice" || !payload.Machine.Running {
		t.Fatalf("Machine = %#v", payload.Machine)
	}
	if !payload.Machine.ContainerTools {
		t.Fatal("ContainerTools = false, want true")
	}
}

func TestMachineStatusText(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{{
			Tenant:         "acme",
			Project:        "default",
			Name:           "codex",
			AppPort:        5173,
			PrivateIP:      "10.248.0.20",
			LinuxUser:      "alice",
			HomeDir:        ".",
			WorkspaceDir:   "workspace",
			ContainerTools: true,
			ExtraSANs:      []string{"app.example.com"},
		}}},
	}, "status", "codex")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Machine: acme/default/codex", "Instance: default-codex", "Private IP: 10.248.0.20", "Linux user: alice", "Container tools: enabled", "Extra SANs: app.example.com"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestStatusRejectsAmbiguousBareMachine(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{
			{Tenant: "acme", Project: "default", Name: "codex"},
			{Tenant: "acme", Project: "website", Name: "codex"},
		}},
	}, "status", "codex")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %q", err)
	}
}

func TestCreateDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "create", "codex", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload machine.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.PrivateIP != "10.248.0.20" {
		t.Fatalf("PrivateIP = %q", payload.PrivateIP)
	}
	if payload.Template != "ai" {
		t.Fatalf("Template = %q", payload.Template)
	}
	if payload.HomeDir != "default" || payload.WorkspaceDir != "default" {
		t.Fatalf("HomeDir/WorkspaceDir = %q/%q, want default", payload.HomeDir, payload.WorkspaceDir)
	}
	if payload.LinuxUser != "acme" {
		t.Fatalf("LinuxUser = %q", payload.LinuxUser)
	}
}

func TestCreateDryRunUsesProjectFromEnv(t *testing.T) {
	t.Setenv("SANDCASTLE_PROJECT", "website")
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "create", "codex", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload machine.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Tenant.Tenant != "acme" || payload.Project != "website" || payload.Name != "codex" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.LinuxUser != "acme" {
		t.Fatalf("LinuxUser = %q", payload.LinuxUser)
	}
}

func TestCreateDryRunSupportsTemplateAndStorageFlags(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "create", "minimal", "--dry-run", "--template", "base", "--home-dir", "shared-home", "--workspace-dir", ".")
	if err != nil {
		t.Fatal(err)
	}
	var payload machine.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Template != "base" {
		t.Fatalf("Template = %q", payload.Template)
	}
	if payload.ImageAlias != scconfig.DefaultBaseImageAlias {
		t.Fatalf("ImageAlias = %q", payload.ImageAlias)
	}
	if payload.HomeDir != "shared-home" || payload.WorkspaceDir != "." {
		t.Fatalf("HomeDir/WorkspaceDir = %q/%q", payload.HomeDir, payload.WorkspaceDir)
	}
}

func TestCreateDryRunSupportsContainerTools(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "create", "codex", "--dry-run", "--container-tools")
	if err != nil {
		t.Fatal(err)
	}
	var payload machine.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.ContainerTools {
		t.Fatal("ContainerTools = false, want true")
	}
	state, err := meta.ParseMachineConfig(payload.MetadataConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !state.ContainerTools {
		t.Fatal("metadata ContainerTools = false, want true")
	}
}

func TestCreateDryRunRejectsUnsafeStorageFlags(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "create", "minimal", "--dry-run", "--home-dir", "../shared")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "must not contain .. path segments") {
		t.Fatalf("error = %q", err)
	}
}

func TestCreateDetachSkipsConnect(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	creator := &fakeMachineCreator{}
	connector := &fakeMachineConnector{}
	applier := &fakeDNSApplier{}
	knownHosts := &fakeKnownHostsManager{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineCreator:   creator,
		machineConnector: connector,
		knownHosts:       knownHosts,
		dnsApplier:       applier,
	}, "create", "codex", "--detach")
	if err != nil {
		t.Fatal(err)
	}
	if creator.plan.InstanceName != "default-codex" {
		t.Fatalf("created instance = %q", creator.plan.InstanceName)
	}
	if connector.called {
		t.Fatal("expected create --detach to skip connect")
	}
	if !applier.called || applier.tenant.Tenant != "acme" {
		t.Fatalf("expected DNS refresh for acme, got %#v", applier)
	}
	if !knownHosts.called || knownHosts.plan.Hostname != "codex.default.acme" || knownHosts.plan.PrivateIP != "10.248.0.20" {
		t.Fatalf("expected known_hosts refresh, got %#v", knownHosts.plan)
	}
}

func TestCreateBackgroundSkipsConnect(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	creator := &fakeMachineCreator{}
	connector := &fakeMachineConnector{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineCreator:   creator,
		machineConnector: connector,
	}, "create", "codex", "--background")
	if err != nil {
		t.Fatal(err)
	}
	if creator.plan.InstanceName != "default-codex" {
		t.Fatalf("created instance = %q", creator.plan.InstanceName)
	}
	if connector.called {
		t.Fatal("expected create --background to skip connect")
	}
}

func TestCreateConnectsAfterCreateByDefault(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	creator := &fakeMachineCreator{}
	connector := &fakeMachineConnector{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineCreator:   creator,
		machineStore:     fakeMachineStatusStore{machines: []meta.Machine{{Tenant: "acme", Project: "default", Name: "codex", PrivateIP: "10.248.0.20", TailscaleIP: "100.64.0.20"}}},
		machineConnector: connector,
	}, "create", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if creator.plan.InstanceName != "default-codex" {
		t.Fatalf("created instance = %q", creator.plan.InstanceName)
	}
	if !connector.called {
		t.Fatal("expected create to connect to machine")
	}
	if connector.plan.InstanceName != "default-codex" {
		t.Fatalf("connected instance = %q", connector.plan.InstanceName)
	}
}

func TestConnectCommandUsesConnector(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := &fakeMachineConnector{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore:     fakeMachineStatusStore{machines: []meta.Machine{{Tenant: "acme", Project: "default", Name: "codex", PrivateIP: "10.248.0.20", TailscaleIP: "100.64.0.20"}}},
		machineConnector: connector,
	}, "connect", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if !connector.called {
		t.Fatal("expected machine connector call")
	}
	if connector.plan.InstanceName != "default-codex" {
		t.Fatalf("connected instance = %q", connector.plan.InstanceName)
	}
	if !connector.plan.Interactive {
		t.Fatal("expected default connect to be interactive")
	}
}

func TestConnectCommandRefreshesKnownHostsWhenUsingPrivateIPFallback(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := &fakeMachineConnector{}
	knownHosts := &fakeKnownHostsManager{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore:     fakeMachineStatusStore{machines: []meta.Machine{{Tenant: "acme", Project: "default", Name: "codex", PrivateIP: "10.248.0.20"}}},
		machineConnector: connector,
		knownHosts:       knownHosts,
	}, "connect", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if !connector.called || connector.plan.SSHHost != "10.248.0.20" {
		t.Fatalf("connector.plan = %#v", connector.plan)
	}
	if !knownHosts.called || knownHosts.plan.Hostname != "codex.default.acme" || knownHosts.plan.PrivateIP != "10.248.0.20" {
		t.Fatalf("expected known_hosts refresh, got %#v", knownHosts.plan)
	}
}

func TestConnectCommandAcceptsExplicitCommand(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := &fakeMachineConnector{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore:     fakeMachineStatusStore{machines: []meta.Machine{{Tenant: "acme", Project: "default", Name: "codex", PrivateIP: "10.248.0.20", TailscaleIP: "100.64.0.20"}}},
		machineConnector: connector,
	}, "connect", "codex", "pwd")
	if err != nil {
		t.Fatal(err)
	}
	if connector.plan.Interactive {
		t.Fatal("expected explicit connect command to be non-interactive")
	}
	if len(connector.plan.Command) != 1 || connector.plan.Command[0] != "pwd" {
		t.Fatalf("Command = %#v", connector.plan.Command)
	}
}

func TestConnectCommandSearchesBareMachineWhenUnique(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := &fakeMachineConnector{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore:     fakeMachineStatusStore{machines: []meta.Machine{{Tenant: "acme", Project: "website", Name: "codex", PrivateIP: "10.248.0.20", TailscaleIP: "100.64.0.20"}}},
		machineConnector: connector,
	}, "connect", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if connector.plan.Project != "website" || connector.plan.InstanceName != "website-codex" {
		t.Fatalf("connector.plan = %#v", connector.plan)
	}
}

func TestConnectCommandRejectsAmbiguousBareMachine(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{
			{Tenant: "acme", Project: "default", Name: "codex"},
			{Tenant: "acme", Project: "website", Name: "codex"},
		}},
		machineConnector: &fakeMachineConnector{},
	}, "connect", "codex")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %q", err)
	}
}

func TestMachineDeleteRequiresConfirmation(t *testing.T) {
	_, err := executeForTest(t, "sandcastle", "delete", "codex")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %q, want --yes hint", err.Error())
	}
}

func TestMachineDeletePromptsOnTerminal(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	controller := &fakeMachineController{}
	_, stderr, err := executeForTestWithConfigAndStderr(t, commandConfig{
		name:  "sandcastle",
		stdin: strings.NewReader("yes\n"),
		stdinIsTerminal: func(io.Reader) bool {
			return true
		},
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{
			machines: []meta.Machine{{Tenant: "acme", Project: "default", Name: "codex"}},
		},
		machineControl: controller,
	}, "delete", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "Delete machine codex? [y/N]") {
		t.Fatalf("stderr = %q", stderr)
	}
	if !controller.called || controller.plan.Action != machine.ActionDelete {
		t.Fatalf("controller.plan = %#v", controller.plan)
	}
}

func TestMachineDeletePromptCanCancel(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	controller := &fakeMachineController{}
	_, stderr, err := executeForTestWithConfigAndStderr(t, commandConfig{
		name:  "sandcastle",
		stdin: strings.NewReader("no\n"),
		stdinIsTerminal: func(io.Reader) bool {
			return true
		},
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineControl: controller,
	}, "delete", "codex")
	if err == nil {
		t.Fatal("expected cancel error")
	}
	if !strings.Contains(stderr, "Delete machine codex? [y/N]") {
		t.Fatalf("stderr = %q", stderr)
	}
	if !strings.Contains(err.Error(), "delete canceled") {
		t.Fatalf("error = %q", err)
	}
	if controller.called {
		t.Fatal("expected canceled delete to skip controller")
	}
}

func TestMachineDeleteCallsExecutor(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	controller := &fakeMachineController{}
	applier := &fakeDNSApplier{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		adminConfig: scconfig.Admin{
			Tenant:                "acme",
			Remote:                scconfig.DefaultRemote,
			StoragePool:           scconfig.DefaultStoragePool,
			CIDRPool:              scconfig.DefaultCIDRPool,
			IncusProjectPrefix:    scconfig.DefaultIncusProjectPrefix,
			InfrastructureProject: scconfig.DefaultInfrastructureProject,
			Images:                scconfig.Images{Base: scconfig.DefaultBaseImageAlias, AI: scconfig.DefaultAIImageAlias},
		},
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{
			machines: []meta.Machine{{Tenant: "acme", Project: "default", Name: "codex"}},
		},
		machineControl: controller,
		dnsApplier:     applier,
	}, "delete", "codex", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !controller.called || controller.plan.Action != machine.ActionDelete {
		t.Fatalf("controller.plan = %#v", controller.plan)
	}
	if !applier.called || applier.tenant.Tenant != "acme" {
		t.Fatalf("expected DNS refresh for acme, got %#v", applier)
	}
}

func TestPortSetRejectsInvalidPort(t *testing.T) {
	_, err := executeForTest(t, "sandcastle", "port", "set", "codex", "bad")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDNSStatusJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "dns", "status", "acme")
	if err != nil {
		t.Fatal(err)
	}
	var payload dns.ApplyResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.DNSAddress != "10.248.0.3" {
		t.Fatalf("DNSAddress = %q", payload.DNSAddress)
	}
}

func TestDNSInstallDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "dns", "install", "acme", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload localdns.Plan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.DNSEndpoint != "10.248.0.3:53" {
		t.Fatalf("DNSEndpoint = %q", payload.DNSEndpoint)
	}
}

func TestDNSInstallUsesCurrentTenantWithoutProject(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	admin := testAdminConfig()
	admin.Tenant = "acme"
	admin.Project = ""
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: admin,
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "dns", "install", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload localdns.Plan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Reference != "acme" {
		t.Fatalf("Reference = %q", payload.Reference)
	}
}

func TestFormatLocalDNSPlanShowsResolverCommands(t *testing.T) {
	output := formatLocalDNSPlan("Install", localdns.Plan{
		Reference:        "acme",
		DNSEndpoint:      "10.248.0.3:53",
		ResolverStrategy: localdns.StrategySystemdResolve,
		ResolverCommands: []localdns.Command{
			{Args: []string{"resolvectl", "dns", "lo", "10.248.0.3:53"}},
			{Args: []string{"resolvectl", "domain", "lo", "~acme"}},
		},
	})
	for _, want := range []string{
		"Resolver: systemd-resolved",
		"Resolver commands:",
		"resolvectl dns lo 10.248.0.3:53",
		"resolvectl domain lo ~acme",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestDNSRefreshRunsLocalDNSExecutor(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeLocalDNSManager{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		localDNS: manager,
	}, "dns", "refresh", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.refreshed {
		t.Fatal("expected local DNS refresh call")
	}
	if manager.plan.DNSEndpoint != "10.248.0.3:53" {
		t.Fatalf("DNSEndpoint = %q", manager.plan.DNSEndpoint)
	}
}

func TestDNSSetupUsesCurrentTenantAndRunsSteps(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	localManager := &fakeLocalDNSManager{}
	applier := &fakeDNSApplier{}
	admin := testAdminConfig()
	admin.Tenant = "acme"
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: admin,
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		dnsApplier: applier,
		localDNS:   localManager,
	}, "dns", "setup")
	if err != nil {
		t.Fatal(err)
	}
	if !applier.called {
		t.Fatal("expected DNS apply call")
	}
	if !localManager.installed || !localManager.refreshed {
		t.Fatalf("local DNS installed=%v refreshed=%v, want both", localManager.installed, localManager.refreshed)
	}
	if localManager.installPlan.Reference != "acme" || localManager.refreshPlan.Reference != "acme" {
		t.Fatalf("plans = %#v %#v, want acme", localManager.installPlan, localManager.refreshPlan)
	}
	for _, want := range []string{"DNS setup: acme", "DNS records: 2", "Resolver:"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestDNSTeardownUsesCurrentTenantAndRunsSteps(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	localManager := &fakeLocalDNSManager{}
	admin := testAdminConfig()
	admin.Tenant = "acme"
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: admin,
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		localDNS: localManager,
	}, "dns", "teardown")
	if err != nil {
		t.Fatal(err)
	}
	if !localManager.uninstalled {
		t.Fatal("expected local DNS uninstall")
	}
	if localManager.uninstallPlan.Reference != "acme" {
		t.Fatalf("uninstall plan = %#v, want acme", localManager.uninstallPlan)
	}
	for _, want := range []string{"DNS teardown: acme", "Resolver:"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestIncusCommandUsesActiveRemoteConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	incusDir := scconfig.RemoteIncusDir("sandcastle-alice")
	if err := os.MkdirAll(incusDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var gotArgs []string
	var gotEnv []string
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: scconfig.Admin{Remote: "sandcastle-alice", Tenant: "acme", IncusProjectPrefix: "sc"},
		incusRunner: func(ctx context.Context, args []string, env []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
			gotArgs = append([]string{}, args...)
			gotEnv = append([]string{}, env...)
			_, _ = stdout.Write([]byte("incus ok"))
			return nil
		},
	}, "incus", "project", "list")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "incus ok" {
		t.Fatalf("stdout = %q", stdout)
	}
	if strings.Join(gotArgs, " ") != "project list" {
		t.Fatalf("args = %#v", gotArgs)
	}
	if !envContains(gotEnv, "INCUS_CONF="+incusDir) {
		t.Fatalf("env missing INCUS_CONF=%s", incusDir)
	}
	if !envContains(gotEnv, "INCUS_PROJECT=sc-acme") {
		t.Fatalf("env missing INCUS_PROJECT=sc-acme")
	}
}

func TestIncusCommandVerboseShowsEnvAndCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("VERBOSE", "1")
	incusDir := scconfig.RemoteIncusDir("sandcastle-alice")
	if err := os.MkdirAll(incusDir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := executeForTestWithConfigAndStderr(t, commandConfig{
		name:        "sandcastle",
		adminConfig: scconfig.Admin{Remote: "sandcastle-alice", Tenant: "acme", IncusProjectPrefix: "sc"},
		incusRunner: func(ctx context.Context, args []string, env []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
			return nil
		},
	}, "incus", "ls")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[verbose] sc incus env: INCUS_CONF=" + incusDir + " INCUS_PROJECT=sc-acme",
		"[verbose] sc incus command: incus ls",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, want %q", stderr, want)
		}
	}
}

func TestIncusCommandRequiresManagedRemoteConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: scconfig.Admin{Remote: "sandcastle-alice"},
	}, "incus", "ls")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "sc remote add") {
		t.Fatalf("error = %q", err)
	}
}

func TestTailscaleUpDryRunRedactsAuthKey(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "tailscale", "up", "acme", "--auth-key", "tskey-secret", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "tskey-secret") {
		t.Fatalf("stdout leaked auth key: %s", stdout)
	}
	var payload tailscale.UpPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.InstanceName != "sc-acme" {
		t.Fatalf("InstanceName = %q", payload.InstanceName)
	}
	if !payload.HasAuthKey {
		t.Fatal("expected HasAuthKey")
	}
}

func TestTailscaleUpDryRunUsesDefaultAdvertiseTag(t *testing.T) {
	t.Setenv("SANDCASTLE_E2E_TAILSCALE_TAG", "")
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "tailscale", "up", "acme", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload tailscale.UpPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.AdvertiseTags) != 1 || payload.AdvertiseTags[0] != tailscale.DefaultAdvertiseTag {
		t.Fatalf("AdvertiseTags = %#v", payload.AdvertiseTags)
	}
	if !strings.Contains(strings.Join(payload.Command, " "), "--advertise-tags="+tailscale.DefaultAdvertiseTag) {
		t.Fatalf("Command = %#v", payload.Command)
	}
}

func TestTailscaleUpUsesCurrentTenant(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "tailscale", "up", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload tailscale.UpPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Reference != "acme" || payload.InstanceName != "sc-acme" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestTailscaleUpDryRunRejectsInvalidAdvertiseTag(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "tailscale", "up", "acme", "--advertise-tag", "sandcastle", "--dry-run")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Tailscale advertise tag") {
		t.Fatalf("error = %q", err)
	}
}

func TestTailscaleUpRunsExecutor(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeTailscaleRunner{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		tailscale: runner,
	}, "tailscale", "up", "acme", "--auth-key", "tskey-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !runner.called {
		t.Fatal("expected tailscale runner call")
	}
	if runner.plan.InstanceName != "sc-acme" {
		t.Fatalf("InstanceName = %q", runner.plan.InstanceName)
	}
	if runner.plan.AuthKey != "tskey-secret" {
		t.Fatalf("AuthKey = %q", runner.plan.AuthKey)
	}
}

func TestTailscaleStatusRunsExecutor(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeTailscaleRunner{status: tailscale.StatusResult{
		Reference: "acme",
		Tailscale: meta.Tailscale{State: "Running", TailscaleIPs: []string{"100.80.12.34"}},
	}}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		tailscale: runner,
	}, "--output", "json", "tailscale", "status", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if !runner.statusCalled {
		t.Fatal("expected tailscale status runner call")
	}
	var payload tailscale.StatusResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Tailscale.State != "Running" {
		t.Fatalf("State = %q", payload.Tailscale.State)
	}
}

func TestTailscaleStatusUsesCurrentTenant(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeTailscaleRunner{status: tailscale.StatusResult{
		Reference: "acme",
		Tailscale: meta.Tailscale{State: "Running"},
	}}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		tailscale: runner,
	}, "tailscale", "status")
	if err != nil {
		t.Fatal(err)
	}
	if !runner.statusCalled || runner.statusPlan.Reference != "acme" {
		t.Fatalf("runner = %#v", runner)
	}
}

func TestTailscaleDownDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "tailscale", "down", "acme", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload tailscale.DownPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Join(payload.Command, " ") != "tailscale down" {
		t.Fatalf("Command = %#v", payload.Command)
	}
}

func TestTailscaleDownUsesCurrentTenant(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "tailscale", "down", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload tailscale.DownPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Reference != "acme" || payload.InstanceName != "sc-acme" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestHostOverrideAddDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		hostMachine: fakeHostMachineStore{},
	}, "--output", "json", "host", "override", "create", "codex", "Example.COM", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload hostoverride.AddPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Hostname != "example.com" {
		t.Fatalf("Hostname = %q", payload.Hostname)
	}
	if payload.IPAddress != "10.248.0.20" {
		t.Fatalf("IPAddress = %q", payload.IPAddress)
	}
}

func TestHostOverrideAddAppliesMachineAndHosts(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeHostOverrideManager{}
	files := &fakeHostFiles{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		hostMachine:   fakeHostMachineStore{},
		hostOverrides: manager,
		hostFiles:     files,
	}, "host", "override", "create", "codex", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.called {
		t.Fatal("expected host override manager call")
	}
	if !files.called {
		t.Fatal("expected hosts file editor call")
	}
	if files.plan.Hostname != "example.com" {
		t.Fatalf("Hostname = %q", files.plan.Hostname)
	}
}

func TestHostOverrideListUsesCurrentTenant(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		hostMachine: fakeHostMachineStore{},
	}, "--output", "json", "host", "override", "list")
	if err != nil {
		t.Fatal(err)
	}
	var payload hostoverride.ListResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Overrides) != 1 || payload.Overrides[0].Hostname != "example.com" {
		t.Fatalf("Overrides = %#v", payload.Overrides)
	}
}

func TestHostOverrideListAcceptsExplicitTenant(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		hostMachine: fakeHostMachineStore{},
	}, "--output", "json", "host", "override", "list", "acme")
	if err != nil {
		t.Fatal(err)
	}
	var payload hostoverride.ListResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Overrides) != 1 || payload.Overrides[0].Hostname != "example.com" {
		t.Fatalf("Overrides = %#v", payload.Overrides)
	}
}

func TestHostOverrideDeleteAppliesMachineAndHosts(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeHostOverrideManager{}
	files := &fakeHostFiles{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		hostMachine:   fakeHostMachineStore{},
		hostOverrides: manager,
		hostFiles:     files,
	}, "host", "override", "delete", "codex", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.deleted {
		t.Fatal("expected host override delete call")
	}
	if !files.deleted {
		t.Fatal("expected hosts file delete call")
	}
}

func TestTrustInstallDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "trust", "install", "acme", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload localtrust.Plan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.CAVolume != tenant.CAVolumeName {
		t.Fatalf("CAVolume = %q", payload.CAVolume)
	}
	if !strings.Contains(payload.Warning, "mint certificates") {
		t.Fatalf("Warning = %q", payload.Warning)
	}
}

func TestTrustInstallUsesCurrentTenantWithoutProject(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	admin := testAdminConfig()
	admin.Tenant = "acme"
	admin.Project = ""
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: admin,
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "trust", "install", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload localtrust.Plan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Reference != "acme" {
		t.Fatalf("Reference = %q", payload.Reference)
	}
	if payload.IncusProject != "sc-acme" {
		t.Fatalf("IncusProject = %q", payload.IncusProject)
	}
}

func TestTrustInstallRunsExecutor(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeLocalTrustManager{}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		localTrust: manager,
	}, "trust", "install", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.installed {
		t.Fatal("expected local trust install call")
	}
	if !strings.Contains(stdout, "Warning: Trusting this tenant CA") {
		t.Fatalf("stdout missing pre-install trust warning: %q", stdout)
	}
	if !strings.Contains(stdout, "install tenant CA trust: acme") {
		t.Fatalf("stdout missing trust result: %q", stdout)
	}
	if manager.plan.IncusProject != "sc-acme" {
		t.Fatalf("IncusProject = %q", manager.plan.IncusProject)
	}
}

func TestTrustUninstallRunsExecutor(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeLocalTrustManager{}
	_, err = executeForTestWithConfig(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		localTrust: manager,
	}, "trust", "uninstall", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.deleted {
		t.Fatal("expected local trust uninstall call")
	}
}

func TestRouteCreateDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: routeAdminConfigForTest(),
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		routeMachine: fakeRouteMachineStore{},
	}, "--output", "json", "route", "create", "App.Example.COM", "codex", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload route.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Hostname != "app.example.com" {
		t.Fatalf("Hostname = %q", payload.Hostname)
	}
	if payload.RoutePort != 5173 {
		t.Fatalf("RoutePort = %d", payload.RoutePort)
	}
}

func TestRouteCreateDryRunTextShowsDNSProofTarget(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: routeAdminConfigForTest(),
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		routeMachine: fakeRouteMachineStore{},
	}, "route", "create", "App.Example.COM", "codex", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Route: app.example.com -> acme/default/codex:5173") {
		t.Fatalf("stdout missing route: %q", stdout)
	}
	if !strings.Contains(stdout, "DNS proof: app.example.com must resolve to 203.0.113.10") {
		t.Fatalf("stdout missing DNS proof target: %q", stdout)
	}
}

func TestRouteCreateRequiresBrokerExecutor(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: routeAdminConfigForTest(),
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		routeMachine: fakeRouteMachineStore{},
	}, "route", "create", "app.example.com", "codex")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "route broker") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestRouteStatusShowsMatchingRoute(t *testing.T) {
	routes := &fakeRouteManager{list: route.ListResult{Routes: []route.Route{
		{Hostname: "app.example.com", TargetReference: "acme/default/codex", RoutePort: 5173},
		{Hostname: "other.example.com", TargetReference: "acme/default/shell", RoutePort: 3000},
	}}}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: routeAdminConfigForTest(),
		routes:      routes,
	}, "route", "status", "App.Example.COM.")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout) != "app.example.com -> acme/default/codex:5173" {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestRouteStatusJSON(t *testing.T) {
	routes := &fakeRouteManager{list: route.ListResult{Routes: []route.Route{
		{Hostname: "app.example.com", TargetReference: "acme/default/codex", RoutePort: 5173},
	}}}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: routeAdminConfigForTest(),
		routes:      routes,
	}, "--output", "json", "route", "status", "app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	var payload route.Route
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Hostname != "app.example.com" || payload.TargetReference != "acme/default/codex" || payload.RoutePort != 5173 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestRouteStatusRequiresExistingRoute(t *testing.T) {
	_, err := executeForTestWithConfig(t, commandConfig{
		name:        "sandcastle",
		adminConfig: routeAdminConfigForTest(),
		routes:      &fakeRouteManager{},
	}, "route", "status", "missing.example.com")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "public route missing.example.com not found") {
		t.Fatalf("error = %q", err)
	}
}

func routeAdminConfigForTest() scconfig.Admin {
	admin := scconfig.LoadAdminFromEnv()
	admin.Tenant = "acme"
	admin.InfrastructureHost = "203.0.113.10"
	return admin
}

func TestRouteManagerFromEnvUsesBrokerClient(t *testing.T) {
	t.Setenv("SANDCASTLE_ROUTE_BROKER_URL", " https://broker.example.com/ ")
	t.Setenv("SANDCASTLE_ROUTE_BROKER_CLIENT_CERT", " /tmp/client.crt ")
	t.Setenv("SANDCASTLE_ROUTE_BROKER_CLIENT_KEY", " /tmp/client.key ")
	t.Setenv("SANDCASTLE_ROUTE_BROKER_INSECURE_SKIP_VERIFY", " 1 ")

	manager := routeManagerFromEnv()
	client, ok := manager.(routebroker.Client)
	if !ok {
		t.Fatalf("manager = %T, want routebroker.Client", manager)
	}
	if client.BaseURL != "https://broker.example.com/" || client.CertFile != "/tmp/client.crt" || client.KeyFile != "/tmp/client.key" {
		t.Fatalf("client = %#v", client)
	}
	if !client.InsecureSkipVerify {
		t.Fatal("expected insecure skip verify flag")
	}
}

func TestRouteManagerFromEnvRequiresBrokerURL(t *testing.T) {
	if manager := routeManagerFromEnv(); manager != nil {
		t.Fatalf("manager = %T, want nil without broker URL", manager)
	}
}

func TestAdminVersion(t *testing.T) {
	stdout, err := executeAdminForTest(t, "sandcastle-admin", "version")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(stdout); got != version {
		t.Fatalf("admin version output = %q, want %q", got, version)
	}
}

func TestAdminVersionHelpUsesAdminWording(t *testing.T) {
	stdout, err := executeAdminForTest(t, "sandcastle-admin", "version", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Print the Sandcastle admin command version") {
		t.Fatalf("admin version help = %q", stdout)
	}
}

func TestAdminTenantListJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name: "sandcastle-admin",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "tenant", "list")
	if err != nil {
		t.Fatal(err)
	}
	var payload tenantListPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Tenants) != 1 {
		t.Fatalf("len(payload.Tenants) = %d, want 1", len(payload.Tenants))
	}
	if payload.Tenants[0].IncusName != "sc-acme" {
		t.Fatalf("IncusName = %q", payload.Tenants[0].IncusName)
	}
}

func TestAdminTenantCreateDryRunJSON(t *testing.T) {
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "tenant", "create", "acme", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload tenant.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.IncusProject != "sc-acme" {
		t.Fatalf("IncusProject = %q", payload.IncusProject)
	}
	if payload.PrivateCIDR != "10.248.0.0/24" {
		t.Fatalf("PrivateCIDR = %q", payload.PrivateCIDR)
	}
}

func TestAdminTenantCreateRequiresExecutor(t *testing.T) {
	_, err := executeAdminForTest(t, "sandcastle-admin", "tenant", "create", "acme")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "executor") {
		t.Fatalf("error = %q, want executor hint", err.Error())
	}
}

func TestAdminTenantCreateRejectsKnownTLD(t *testing.T) {
	creator := &fakeTenantCreator{}
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:          "sandcastle-admin",
		tenantCreator: creator,
	}, "tenant", "create", "test")
	if err == nil {
		t.Fatal("expected known TLD error")
	}
	if !strings.Contains(err.Error(), "denied special-use suffix") {
		t.Fatalf("error = %q", err.Error())
	}
	if creator.called {
		t.Fatal("creator should not be called for invalid tenant")
	}
}

func TestAdminMachineListJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name: "sandcastle-admin",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{{
			Tenant: "acme", Project: "default", Name: "codex", PrivateIP: "10.248.0.20", AppPort: 3000,
		}, {
			Tenant: "acme", Project: "website", Name: "codex", PrivateIP: "10.248.0.21", AppPort: 3000,
		}}},
	}, "--output", "json", "list", "acme")
	if err != nil {
		t.Fatal(err)
	}
	var payload listPayload
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Tenant.Tenant != "acme" || !payload.AllProjects || len(payload.Machines) != 2 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestAdminMachineListProjectFilters(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name: "sandcastle-admin",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore: fakeMachineStatusStore{machines: []meta.Machine{{
			Tenant: "acme", Project: "default", Name: "builder", PrivateIP: "10.248.0.20", AppPort: 3000,
		}, {
			Tenant: "acme", Project: "website", Name: "codex", PrivateIP: "10.248.0.21", AppPort: 3000,
		}}},
	}, "list", "acme/website")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "website") || !strings.Contains(stdout, "codex") {
		t.Fatalf("stdout = %q, want website/codex", stdout)
	}
	if strings.Contains(stdout, "builder") {
		t.Fatalf("stdout = %q, want project filter to hide default/builder", stdout)
	}
}

func TestAdminMachineCreateDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name: "sandcastle-admin",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "create", "acme/codex", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload machine.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Tenant.Tenant != "acme" || payload.Project != "default" || payload.InstanceName != "default-codex" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Reference != "acme/default/codex" {
		t.Fatalf("Reference = %q", payload.Reference)
	}
}

func TestAdminMachineCreateExplicitProjectDryRunJSON(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name: "sandcastle-admin",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
	}, "--output", "json", "create", "acme/website/codex", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload machine.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Project != "website" || payload.InstanceName != "website-codex" || payload.Reference != "acme/website/codex" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestAdminMachineConnectUsesTenantRef(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := &fakeMachineConnector{}
	_, err = executeAdminForTestWithConfig(t, commandConfig{
		name: "sandcastle-admin",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore:     fakeMachineStatusStore{machines: []meta.Machine{{Tenant: "acme", Project: "default", Name: "codex", PrivateIP: "10.248.0.20", TailscaleIP: "100.64.0.20"}}},
		machineConnector: connector,
	}, "connect", "acme/codex", "pwd")
	if err != nil {
		t.Fatal(err)
	}
	if !connector.called || connector.plan.Reference != "acme/default/codex" || connector.plan.InstanceName != "default-codex" {
		t.Fatalf("connector.plan = %#v", connector.plan)
	}
}

func TestAdminMachineDeleteRequiresConfirmation(t *testing.T) {
	_, err := executeAdminForTest(t, "sandcastle-admin", "delete", "acme/codex")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %q", err)
	}
}

func TestAdminMachineDeleteCallsExecutor(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	controller := &fakeMachineController{}
	applier := &fakeDNSApplier{}
	_, err = executeAdminForTestWithConfig(t, commandConfig{
		name: "sandcastle-admin",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineControl: controller,
		dnsApplier:     applier,
	}, "delete", "acme/codex", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !controller.called || controller.plan.Reference != "acme/default/codex" || controller.plan.InstanceName != "default-codex" || controller.plan.Action != machine.ActionDelete {
		t.Fatalf("controller.plan = %#v", controller.plan)
	}
	if !applier.called || applier.tenant.Tenant != "acme" {
		t.Fatalf("expected DNS refresh for acme, got %#v", applier)
	}
}

func TestAdminTLDRefreshWritesSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tlds":
			_, _ = w.Write([]byte("# Version 2026050700\nCOM\nORG\n"))
		case "/special-use":
			_, _ = w.Write([]byte("Name,Reference\nLOCAL.,[RFC6762]\nTEST.,[RFC6761]\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	output := filepath.Join(dir, "tld_snapshot_generated.go")
	specialUseOutput := filepath.Join(dir, "special_use_snapshot_generated.go")
	stdout, err := executeAdminForTest(t, "sandcastle-admin", "tld", "refresh", "--source-url", server.URL+"/tlds", "--output-file", output, "--special-use-source-url", server.URL+"/special-use", "--special-use-output-file", specialUseOutput)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Refreshed 2 public TLDs") {
		t.Fatalf("stdout = %q", stdout)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"com": true`) {
		t.Fatalf("content = %s", string(content))
	}
	specialUseContent, err := os.ReadFile(specialUseOutput)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(specialUseContent), `"local": true`) {
		t.Fatalf("special use content = %s", string(specialUseContent))
	}
}

func TestAdminTLDRefreshDryRunJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tlds":
			_, _ = w.Write([]byte("COM\nORG\n"))
		case "/special-use":
			_, _ = w.Write([]byte("Name,Reference\nLOCAL.,[RFC6762]\nTEST.,[RFC6761]\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	output := filepath.Join(dir, "tld_snapshot_generated.go")
	specialUseOutput := filepath.Join(dir, "special_use_snapshot_generated.go")
	stdout, err := executeAdminForTest(t, "sandcastle-admin", "--output", "json", "tld", "refresh", "--source-url", server.URL+"/tlds", "--output-file", output, "--special-use-source-url", server.URL+"/special-use", "--special-use-output-file", specialUseOutput, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload domain.DenyListRefreshResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.TLD.Count != 2 || payload.TLD.Written || payload.SpecialUse.Count != 2 || payload.SpecialUse.Written {
		t.Fatalf("payload = %#v", payload)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("expected dry run not to write output, stat err = %v", err)
	}
	if _, err := os.Stat(specialUseOutput); !os.IsNotExist(err) {
		t.Fatalf("expected dry run not to write special-use output, stat err = %v", err)
	}
}

func TestAdminTenantDeleteRequiresConfirmation(t *testing.T) {
	_, err := executeAdminForTest(t, "sandcastle-admin", "tenant", "delete", "acme")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %q, want --yes hint", err.Error())
	}
}

func TestAdminInfraCreateDryRunJSON(t *testing.T) {
	setAdminRuntimeBinaryForTest(t)
	t.Setenv("USER", "localuser")
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "infra", "create", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload infra.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Project != scconfig.DefaultInfrastructureProject {
		t.Fatalf("Project = %q", payload.Project)
	}
	if payload.CaddyInstance != "sc-caddy" {
		t.Fatalf("CaddyInstance = %q", payload.CaddyInstance)
	}
	if payload.RouteBrokerInstance != infra.RouteBrokerName {
		t.Fatalf("RouteBrokerInstance = %q", payload.RouteBrokerInstance)
	}
	if payload.DefaultUnixUser != "localuser" {
		t.Fatalf("DefaultUnixUser = %q", payload.DefaultUnixUser)
	}
}

func TestAdminInfraCreateVerbosePrintsSeedFile(t *testing.T) {
	setAdminRuntimeBinaryForTest(t)
	t.Setenv("VERBOSE", "1")
	_, stderr, err := executeAdminForTestWithConfigAndStderr(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "infra", "create", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "[verbose] seed file:") {
		t.Fatalf("stderr missing seed file:\n%s", stderr)
	}
	if !strings.Contains(stderr, ".seed.yml") {
		t.Fatalf("stderr missing seed path:\n%s", stderr)
	}
}

func TestAdminInfraCreateUsernameFlagOverridesLocalUser(t *testing.T) {
	setAdminRuntimeBinaryForTest(t)
	t.Setenv("USER", "localuser")
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "infra", "create", "--dry-run", "--username", "override")
	if err != nil {
		t.Fatal(err)
	}
	var payload infra.CreatePlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.DefaultUnixUser != "override" {
		t.Fatalf("DefaultUnixUser = %q", payload.DefaultUnixUser)
	}
}

func TestAdminInfraCreateCallsExecutor(t *testing.T) {
	setAdminRuntimeBinaryForTest(t)
	creator := &fakeInfraCreator{}
	admin := scconfig.LoadAdminFromEnv()
	admin.InfrastructureTLSMode = "acme"
	_, stderr, err := executeAdminForTestWithConfigAndStderr(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  admin,
		infraCreator: creator,
	}, "infra", "create")
	if err != nil {
		t.Fatal(err)
	}
	if !creator.called {
		t.Fatal("expected infrastructure creator to be called")
	}
	if creator.plan.Project != scconfig.DefaultInfrastructureProject {
		t.Fatalf("Project = %q", creator.plan.Project)
	}
	if !strings.Contains(stderr, "Sandcastle infrastructure configuration") {
		t.Fatalf("stderr missing config banner:\n%s", stderr)
	}
}

func TestAdminInfraCreatePrintsConfigBanner(t *testing.T) {
	creator := &fakeInfraCreator{}
	adminBin := setAdminRuntimeBinaryForTest(t)
	admin := scconfig.LoadAdminFromEnv()
	admin.Remote = "big"
	admin.InfrastructureProject = "castle-infra"
	admin.IncusProjectPrefix = "castle"
	admin.InfrastructureTLSMode = "internal"
	admin.AuthHostname = "auth.example.com"
	admin.AuthGitHubClientID = "client-id"
	admin.AuthGitHubClientSecret = "secret-value"
	admin.AuthAdminGitHubUsers = []string{"octocat", "hubot"}
	admin.RouteBrokerIncusSocket = "/var/lib/incus/unix.socket"
	_, stderr, err := executeAdminForTestWithConfigAndStderr(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  admin,
		infraCreator: creator,
		localTrust:   &fakeLocalTrustManager{},
	}, "infra", "create")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Sandcastle infrastructure configuration",
		"SANDCASTLE_REMOTE=big",
		"SANDCASTLE_INCUS_PROJECT_PREFIX=castle",
		"SANDCASTLE_INFRA_PROJECT=castle-infra",
		"SANDCASTLE_INFRA_TLS_MODE=internal",
		"SANDCASTLE_AUTH_HOSTNAME=auth.example.com",
		"SANDCASTLE_AUTH_GITHUB_CLIENT_ID=client-id",
		"SANDCASTLE_AUTH_GITHUB_CLIENT_SECRET=set (redacted)",
		"SANDCASTLE_AUTH_ADMIN_GITHUB_USERS=octocat,hubot",
		"SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET=/var/lib/incus/unix.socket",
		"SANDCASTLE_ADMIN_BIN=" + adminBin,
		"selected admin binary=" + adminBin,
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "secret-value") {
		t.Fatalf("stderr leaked secret:\n%s", stderr)
	}
}

func TestAdminInfraCreateInternalTLSInstallsDebugCA(t *testing.T) {
	setAdminRuntimeBinaryForTest(t)
	t.Setenv("SANDCASTLE_INFRA_CA_DIR", t.TempDir())
	creator := &fakeInfraCreator{}
	manager := &fakeLocalTrustManager{}
	admin := scconfig.LoadAdminFromEnv()
	admin.InfrastructureTLSMode = "internal"
	stdout, _, err := executeAdminForTestWithConfigAndStderr(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  admin,
		infraCreator: creator,
		localTrust:   manager,
	}, "infra", "create")
	if err != nil {
		t.Fatal(err)
	}
	if !creator.called {
		t.Fatal("expected infrastructure creator to be called")
	}
	if !manager.installed {
		t.Fatal("expected infrastructure debug CA trust install")
	}
	if !infraPlanHasFile(creator.plan, "sc-caddy", infra.CaddyPKIRootCertPath) {
		t.Fatalf("expected persisted infrastructure root cert in plan")
	}
	if !infraPlanHasFile(creator.plan, "sc-caddy", infra.CaddyPKIRootKeyPath) {
		t.Fatalf("expected persisted infrastructure root key in plan")
	}
	if manager.plan.IncusProject != scconfig.DefaultInfrastructureProject {
		t.Fatalf("IncusProject = %q", manager.plan.IncusProject)
	}
	if manager.plan.Instance != "sc-caddy" {
		t.Fatalf("Instance = %q", manager.plan.Instance)
	}
	if !strings.Contains(stdout, "Warning: Trusting the infrastructure debug CA") {
		t.Fatalf("stdout missing warning:\n%s", stdout)
	}
	if !strings.Contains(stdout, "install infrastructure debug CA trust") {
		t.Fatalf("stdout missing trust result:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Infrastructure project: "+scconfig.DefaultInfrastructureProject) {
		t.Fatalf("stdout missing create result:\n%s", stdout)
	}
}

func TestAdminInfraCreateContinuesWhenDebugCATrustInstallFails(t *testing.T) {
	setAdminRuntimeBinaryForTest(t)
	t.Setenv("SANDCASTLE_INFRA_CA_DIR", t.TempDir())
	manager := &fakeLocalTrustManager{installErr: fmt.Errorf("authorization denied")}
	admin := scconfig.LoadAdminFromEnv()
	admin.InfrastructureTLSMode = "internal"
	stdout, stderr, err := executeAdminForTestWithConfigAndStderr(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  admin,
		infraCreator: &fakeInfraCreator{},
		localTrust:   manager,
	}, "infra", "create")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.installed {
		t.Fatal("expected infrastructure debug CA trust install attempt")
	}
	if !strings.Contains(stderr, "infrastructure debug CA trust install failed") {
		t.Fatalf("stderr missing trust warning:\n%s", stderr)
	}
	if !strings.Contains(stdout, "Infrastructure project: "+scconfig.DefaultInfrastructureProject) {
		t.Fatalf("stdout missing create result:\n%s", stdout)
	}
}

func TestAdminInfraCreateACMEDoesNotInstallDebugCA(t *testing.T) {
	setAdminRuntimeBinaryForTest(t)
	creator := &fakeInfraCreator{}
	manager := &fakeLocalTrustManager{}
	admin := scconfig.LoadAdminFromEnv()
	admin.InfrastructureTLSMode = "acme"
	_, _, err := executeAdminForTestWithConfigAndStderr(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  admin,
		infraCreator: creator,
		localTrust:   manager,
	}, "infra", "create")
	if err != nil {
		t.Fatal(err)
	}
	if manager.installed {
		t.Fatal("did not expect infrastructure debug CA trust install")
	}
}

func TestAdminInfraCreateACMERestoresCaddyDataArchiveWhenPresent(t *testing.T) {
	setAdminRuntimeBinaryForTest(t)
	archive := t.TempDir() + "/caddy-data.tgz"
	if err := os.WriteFile(archive, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SANDCASTLE_INFRA_CADDY_DATA_ARCHIVE", archive)
	creator := &fakeInfraCreator{}
	admin := scconfig.LoadAdminFromEnv()
	admin.InfrastructureTLSMode = "acme"
	_, _, err := executeAdminForTestWithConfigAndStderr(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  admin,
		infraCreator: creator,
	}, "infra", "create")
	if err != nil {
		t.Fatal(err)
	}
	if creator.plan.CaddyDataArchivePath != archive {
		t.Fatalf("CaddyDataArchivePath = %q, want %q", creator.plan.CaddyDataArchivePath, archive)
	}
}

func TestAdminInfraGenSeedWritesDefaultDeploymentSeed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	admin := scconfig.LoadAdminFromEnv()
	admin.Remote = "big"
	admin.InfrastructureProject = "big-infra"
	admin.AuthHostname = "auth.example.com"
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: admin,
	}, "infra", "gen-seed", "--name", "lab", "--username", "alice")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config", "sandcastle", "lab.seed.yml")
	seed, ok, err := infra.LoadSeed(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected seed file")
	}
	if seed.Deployment != "lab" || seed.Infra.Remote != "big" {
		t.Fatalf("seed = %#v", seed)
	}
	if !strings.Contains(stdout, "Generated infrastructure seed") || !strings.Contains(stdout, path) {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestAdminInfraCreateUsesSeedAndEnvOverrides(t *testing.T) {
	setAdminRuntimeBinaryForTest(t)
	t.Setenv("SANDCASTLE_INFRA_PROJECT", "env-infra")
	seedPath := filepath.Join(t.TempDir(), "lab.seed.yml")
	admin := scconfig.AdminDefaults()
	admin.Remote = "seed-remote"
	admin.InfrastructureProject = "seed-infra"
	admin.AuthHostname = "auth.example.com"
	seed := infra.SeedFromAdmin("lab", admin)
	if err := infra.SaveSeed(seedPath, seed); err != nil {
		t.Fatal(err)
	}
	creator := &fakeInfraCreator{}
	_, _, err := executeAdminForTestWithConfigAndStderr(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  scconfig.AdminDefaults(),
		infraCreator: creator,
	}, "infra", "create", "--seed", seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if creator.plan.Remote != "seed-remote" {
		t.Fatalf("Remote = %q", creator.plan.Remote)
	}
	if creator.plan.Project != "env-infra" {
		t.Fatalf("Project = %q", creator.plan.Project)
	}
	if creator.plan.DefaultUnixUser == "seeduser" {
		t.Fatalf("DefaultUnixUser unexpectedly came from seed: %q", creator.plan.DefaultUnixUser)
	}
}

func TestAdminInfraCreateRestoresEmbeddedSeedCaddyData(t *testing.T) {
	setAdminRuntimeBinaryForTest(t)
	seedPath := filepath.Join(t.TempDir(), "lab.seed.yml")
	admin := scconfig.AdminDefaults()
	admin.Remote = "seed-remote"
	admin.AuthHostname = "auth.example.com"
	seed := infra.SeedFromAdmin("lab", admin)
	seed = infra.EmbedCaddyDataArchive(seed, "auth.example.com", []byte("archive"))
	if err := infra.SaveSeed(seedPath, seed); err != nil {
		t.Fatal(err)
	}
	creator := &fakeInfraCreator{}
	_, _, err := executeAdminForTestWithConfigAndStderr(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  scconfig.AdminDefaults(),
		infraCreator: creator,
	}, "infra", "create", "--seed", seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if creator.plan.CaddyDataArchivePath == "" {
		t.Fatal("expected embedded archive to be restored through a temp archive path")
	}
}

func TestAdminInfraCreateCapturesCaddyDataIntoSeed(t *testing.T) {
	setAdminRuntimeBinaryForTest(t)
	seedPath := filepath.Join(t.TempDir(), "lab.seed.yml")
	admin := scconfig.AdminDefaults()
	admin.Remote = "seed-remote"
	admin.AuthHostname = "auth.example.com"
	seed := infra.SeedFromAdmin("lab", admin)
	if err := infra.SaveSeed(seedPath, seed); err != nil {
		t.Fatal(err)
	}
	creator := &fakeInfraCreator{}
	exporter := &fakeInfraCaddyDataExporter{archiveContent: []byte("captured")}
	_, _, err := executeAdminForTestWithConfigAndStderr(t, commandConfig{
		name:           "sandcastle-admin",
		adminConfig:    scconfig.AdminDefaults(),
		infraCreator:   creator,
		infraCaddyData: exporter,
	}, "infra", "create", "--seed", seedPath)
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := infra.LoadSeed(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected seed")
	}
	data, ok, err := infra.CaddyDataArchiveBytes(loaded, "auth.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || string(data) != "captured" {
		t.Fatalf("captured archive = %q ok=%v", string(data), ok)
	}
}

func TestAdminInfraCreateBuildsAndUploadsDefaultImages(t *testing.T) {
	setAdminRuntimeBinaryForTest(t)
	creator := &fakeInfraCreator{}
	builder := &fakeImageBuilder{}
	uploader := &fakeImageUploader{}
	_, _, err := executeAdminForTestWithConfigAndStderr(t, commandConfig{
		name:          "sandcastle-admin",
		adminConfig:   scconfig.LoadAdminFromEnv(),
		infraCreator:  creator,
		imageBuilder:  builder,
		imageUploader: uploader,
	}, "infra", "create")
	if err != nil {
		t.Fatal(err)
	}
	if !builder.called || len(builder.plans) != 2 {
		t.Fatalf("builder called=%v plans=%d", builder.called, len(builder.plans))
	}
	if !uploader.called || len(uploader.plans) != 2 {
		t.Fatalf("uploader called=%v plans=%d", uploader.called, len(uploader.plans))
	}
	if builder.plans[0].Template != "base" || builder.plans[1].Template != "ai" {
		t.Fatalf("build plans = %#v", builder.plans)
	}
	if builder.plans[0].Platform != "linux/amd64" || builder.plans[1].Platform != "linux/amd64" {
		t.Fatalf("build platforms = %#v", builder.plans)
	}
	if uploader.plans[0].Alias != scconfig.DefaultBaseImageAlias || uploader.plans[1].Alias != scconfig.DefaultAIImageAlias {
		t.Fatalf("upload plans = %#v", uploader.plans)
	}
}

func TestAdminInfraCreateBuildPlatformEnvOverride(t *testing.T) {
	t.Setenv("SANDCASTLE_IMAGE_PLATFORM", "linux/arm64")
	setAdminRuntimeBinaryForTest(t)
	builder := &fakeImageBuilder{}
	_, _, err := executeAdminForTestWithConfigAndStderr(t, commandConfig{
		name:          "sandcastle-admin",
		adminConfig:   scconfig.LoadAdminFromEnv(),
		infraCreator:  &fakeInfraCreator{},
		imageBuilder:  builder,
		imageUploader: &fakeImageUploader{},
	}, "infra", "create")
	if err != nil {
		t.Fatal(err)
	}
	if len(builder.plans) != 2 || builder.plans[0].Platform != "linux/arm64" || builder.plans[1].Platform != "linux/arm64" {
		t.Fatalf("build plans = %#v", builder.plans)
	}
}

func TestAdminInfraCreateSkipsImageBuildForFullOCIRefs(t *testing.T) {
	setAdminRuntimeBinaryForTest(t)
	admin := scconfig.LoadAdminFromEnv()
	admin.Images.Base = "oci:registry.example.com/sandcastle/base:latest"
	admin.Images.AI = "oci:registry.example.com/sandcastle/ai:latest"
	builder := &fakeImageBuilder{}
	uploader := &fakeImageUploader{}
	_, _, err := executeAdminForTestWithConfigAndStderr(t, commandConfig{
		name:          "sandcastle-admin",
		adminConfig:   admin,
		infraCreator:  &fakeInfraCreator{},
		imageBuilder:  builder,
		imageUploader: uploader,
	}, "infra", "create")
	if err != nil {
		t.Fatal(err)
	}
	if builder.called || uploader.called {
		t.Fatalf("expected no image preparation, builder=%v uploader=%v", builder.called, uploader.called)
	}
}

func TestAdminInfraCertExportRunsExecutor(t *testing.T) {
	exporter := &fakeInfraCaddyDataExporter{}
	archive := t.TempDir() + "/caddy-data.tgz"
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:           "sandcastle-admin",
		adminConfig:    scconfig.LoadAdminFromEnv(),
		infraCaddyData: exporter,
	}, "infra", "cert", "export", "--archive", archive)
	if err != nil {
		t.Fatal(err)
	}
	if !exporter.called {
		t.Fatal("expected Caddy data exporter call")
	}
	if exporter.plan.ArchivePath != archive {
		t.Fatalf("ArchivePath = %q, want %q", exporter.plan.ArchivePath, archive)
	}
	if exporter.plan.Instance != "sc-caddy" || exporter.plan.SourcePath != infra.CaddyDataDir {
		t.Fatalf("plan = %#v", exporter.plan)
	}
	if !strings.Contains(stdout, "Exported infrastructure Caddy ACME data") || !strings.Contains(stdout, archive) {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestAdminInfraCertExportDryRunJSON(t *testing.T) {
	archive := t.TempDir() + "/caddy-data.tgz"
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "infra", "cert", "export", "--archive", archive, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload infra.CaddyDataExportPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ArchivePath != archive || payload.SourcePath != infra.CaddyDataDir {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestAdminInfraCreateRequiresExecutor(t *testing.T) {
	setAdminRuntimeBinaryForTest(t)
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "infra", "create")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "infrastructure creation executor") {
		t.Fatalf("error = %q", err)
	}
}

func setAdminRuntimeBinaryForTest(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	adminBin := t.TempDir() + "/sc-adm"
	if err := os.WriteFile(adminBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SANDCASTLE_ADMIN_BIN", adminBin)
	return adminBin
}

func TestAdminInfraDeleteCallsExecutor(t *testing.T) {
	deleter := &fakeInfraDeleter{}
	_, stderr, err := executeAdminForTestWithConfigAndStderr(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		infraDeleter: deleter,
	}, "infra", "delete")
	if err != nil {
		t.Fatal(err)
	}
	if !deleter.called {
		t.Fatal("expected infrastructure deleter to be called")
	}
	if deleter.plan.Project != scconfig.DefaultInfrastructureProject {
		t.Fatalf("Project = %q", deleter.plan.Project)
	}
	if !strings.Contains(stderr, "Sandcastle infrastructure configuration") {
		t.Fatalf("stderr missing config banner:\n%s", stderr)
	}
}

func TestAdminInfraDeletePurgeCallsExecutorWithPurge(t *testing.T) {
	deleter := &fakeInfraDeleter{}
	stdout, stderr, err := executeAdminForTestWithConfigAndStderr(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		infraDeleter: deleter,
	}, "infra", "delete", "--purge")
	if err != nil {
		t.Fatal(err)
	}
	if !deleter.plan.PurgeData {
		t.Fatalf("plan = %#v", deleter.plan)
	}
	if !strings.Contains(stdout, "purged Sandcastle data") {
		t.Fatalf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "Sandcastle infrastructure configuration") {
		t.Fatalf("stderr missing config banner:\n%s", stderr)
	}
}

func TestAdminInfraTrustInstallRunsExecutor(t *testing.T) {
	manager := &fakeLocalTrustManager{}
	admin := scconfig.LoadAdminFromEnv()
	admin.InfrastructureTLSMode = "internal"
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: admin,
		localTrust:  manager,
	}, "infra", "trust", "install")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.installed {
		t.Fatal("expected local trust install call")
	}
	if manager.plan.IncusProject != scconfig.DefaultInfrastructureProject {
		t.Fatalf("IncusProject = %q", manager.plan.IncusProject)
	}
	if manager.plan.Instance != "sc-caddy" {
		t.Fatalf("Instance = %q", manager.plan.Instance)
	}
	if !strings.Contains(stdout, "Warning: Trusting the infrastructure debug CA") {
		t.Fatalf("stdout missing warning: %q", stdout)
	}
	if !strings.Contains(stdout, "install infrastructure debug CA trust") {
		t.Fatalf("stdout missing result: %q", stdout)
	}
}

func TestAdminInfraTrustInstallDryRunJSON(t *testing.T) {
	admin := scconfig.LoadAdminFromEnv()
	admin.InfrastructureTLSMode = "internal"
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: admin,
	}, "--output", "json", "infra", "trust", "install", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload localtrust.Plan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Reference != "infrastructure" || payload.Instance != "sc-caddy" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestAdminImageSyncDryRunJSON(t *testing.T) {
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "image", "sync", "sandcastle/base:debian-13", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload images.SyncPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Template != "base" {
		t.Fatalf("Template = %q", payload.Template)
	}
	if payload.Alias != scconfig.DefaultBaseImageAlias {
		t.Fatalf("Alias = %q", payload.Alias)
	}
}

func TestAdminImageBuildDryRunJSON(t *testing.T) {
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "image", "build", "base", "--tag", "sandcastle/base:debian-13", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload images.BuildPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Template != "base" || payload.Tag != "sandcastle/base:debian-13" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestAdminImageBuildRequiresPinnedAIVersions(t *testing.T) {
	_, err := executeAdminForTest(t, "sandcastle-admin", "image", "build", "ai", "--dry-run")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "codex-version") {
		t.Fatalf("error = %q", err)
	}
}

func TestAdminImageBuildCallsExecutor(t *testing.T) {
	builder := &fakeImageBuilder{}
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		imageBuilder: builder,
	}, "image", "build", "base", "--tag", "sandcastle/base:debian-13")
	if err != nil {
		t.Fatal(err)
	}
	if !builder.called {
		t.Fatal("expected image builder to be called")
	}
	if builder.plan.Tag != "sandcastle/base:debian-13" {
		t.Fatalf("Tag = %q", builder.plan.Tag)
	}
}

func TestAdminImageImportDryRunJSON(t *testing.T) {
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "image", "import", "base", "oci:sandcastle/base:debian-13", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload images.ImportPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Alias != scconfig.DefaultBaseImageAlias {
		t.Fatalf("Alias = %q", payload.Alias)
	}
	if !strings.Contains(strings.Join(payload.Command, " "), "image copy oci:sandcastle/base:debian-13") {
		t.Fatalf("Command = %#v", payload.Command)
	}
}

func TestAdminImageImportCallsExecutor(t *testing.T) {
	importer := &fakeImageImporter{}
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:          "sandcastle-admin",
		adminConfig:   scconfig.LoadAdminFromEnv(),
		imageImporter: importer,
	}, "image", "import", "ai", "oci:sandcastle/ai:debian-13")
	if err != nil {
		t.Fatal(err)
	}
	if !importer.called {
		t.Fatal("expected image importer to be called")
	}
	if importer.plan.Alias != scconfig.DefaultAIImageAlias {
		t.Fatalf("Alias = %q", importer.plan.Alias)
	}
}

func TestAdminImageSyncCallsExecutor(t *testing.T) {
	manager := &fakeImageManager{result: images.SyncResult{Fingerprint: "abc123", Action: "created"}}
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		imageManager: manager,
	}, "image", "sync", "sandcastle/ai:debian-13")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.called {
		t.Fatal("expected image manager to be called")
	}
	if manager.plan.Alias != scconfig.DefaultAIImageAlias {
		t.Fatalf("Alias = %q", manager.plan.Alias)
	}
}

func TestAdminTenantGrantDryRunJSON(t *testing.T) {
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "--output", "json", "tenant", "grant", "acme", "alice", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var payload usertrust.UserPlan
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.CertificateName != "sandcastle-alice" {
		t.Fatalf("CertificateName = %q", payload.CertificateName)
	}
	if len(payload.Projects) != 1 || payload.Projects[0] != "sc-acme" {
		t.Fatalf("Projects = %#v", payload.Projects)
	}
}

func TestAdminTenantGrantCallsTrustManager(t *testing.T) {
	manager := &fakeTrustManager{}
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		trustManager: manager,
	}, "tenant", "grant", "acme", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.grantCalled || manager.plan.User != "alice" || len(manager.plan.Projects) != 1 || manager.plan.Projects[0] != "sc-acme" {
		t.Fatalf("manager = %#v", manager)
	}
}

func TestAdminTenantRevokeCallsTrustManager(t *testing.T) {
	manager := &fakeTrustManager{}
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		trustManager: manager,
	}, "tenant", "revoke", "acme", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.revokeCalled || manager.plan.User != "alice" || len(manager.plan.Projects) != 1 || manager.plan.Projects[0] != "sc-acme" {
		t.Fatalf("manager = %#v", manager)
	}
}

func TestAdminTenantUsersListsTrustUsers(t *testing.T) {
	manager := &fakeTrustManager{tenantUsers: usertrust.TenantUsersResult{
		Tenant:       "acme",
		IncusProject: "sc-acme",
		Users:        []string{"alice", "bob"},
	}}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		adminConfig:  scconfig.LoadAdminFromEnv(),
		trustManager: manager,
	}, "tenant", "users", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.usersCalled {
		t.Fatal("expected tenant users manager call")
	}
	if !strings.Contains(stdout, "Users: alice, bob") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestAdminUserCreateDryRunShowsRemoteName(t *testing.T) {
	stdout, err := executeAdminForTest(t, "sandcastle-admin", "user", "create", "alice", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Remote: sandcastle-alice") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestAdminTenantGrantRejectsInvalidTenantRef(t *testing.T) {
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		adminConfig: scconfig.LoadAdminFromEnv(),
	}, "tenant", "grant", "bob/default", "alice", "--dry-run")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid tenant") {
		t.Fatalf("error = %q", err)
	}
}

func TestAdminUserTokenShowsBootstrapCommands(t *testing.T) {
	manager := &fakeTrustManager{token: "certificate-add-token"}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		trustManager: manager,
	}, "user", "token", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.tokenCalled {
		t.Fatal("expected token manager to be called")
	}
	for _, want := range []string{
		"Remote: sandcastle-alice",
		"sc remote add sandcastle-alice certificate-add-token",
		"sc config set tenant <tenant>",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestAdminUserTokenSupportsPreGrantedTenant(t *testing.T) {
	manager := &fakeTrustManager{token: "certificate-add-token"}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		trustManager: manager,
	}, "user", "token", "alice", "--tenant", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.tokenCalled {
		t.Fatal("expected token manager to be called")
	}
	if len(manager.plan.Projects) != 1 || manager.plan.Projects[0] != "sc-acme" {
		t.Fatalf("Projects = %#v", manager.plan.Projects)
	}
	if !strings.Contains(stdout, "sc remote add sandcastle-alice certificate-add-token --tenant acme") {
		t.Fatalf("stdout = %q", stdout)
	}
	if strings.Contains(stdout, "sc config set tenant") {
		t.Fatalf("stdout = %q, want no post-grant tenant hint", stdout)
	}
}

func TestAdminUserTokenJSONIncludesRemoteName(t *testing.T) {
	manager := &fakeTrustManager{token: "certificate-add-token"}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		trustManager: manager,
	}, "--output", "json", "user", "token", "alice")
	if err != nil {
		t.Fatal(err)
	}
	var payload usertrust.TokenResult
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.RemoteName != "sandcastle-alice" {
		t.Fatalf("RemoteName = %q", payload.RemoteName)
	}
}

func TestAdminUserDeleteCallsTrustManager(t *testing.T) {
	manager := &fakeTrustManager{}
	stdout, err := executeAdminForTestWithConfig(t, commandConfig{
		name:         "sandcastle-admin",
		trustManager: manager,
	}, "user", "delete", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.deleteCalled {
		t.Fatal("expected delete manager to be called")
	}
	if manager.plan.CertificateName != "sandcastle-alice" {
		t.Fatalf("plan = %#v", manager.plan)
	}
	if !strings.Contains(stdout, "Deleted restricted user certificate: sandcastle-alice") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestAdminRouteBrokerServeCallsRunner(t *testing.T) {
	runner := &fakeRouteBrokerRunner{}
	_, err := executeAdminForTestWithConfig(t, commandConfig{
		name:        "sandcastle-admin",
		routeBroker: runner,
	}, "route-broker", "serve", "--listen", "127.0.0.1:9443", "--cert", "/tmp/broker.crt", "--key", "/tmp/broker.key")
	if err != nil {
		t.Fatal(err)
	}
	if !runner.called {
		t.Fatal("expected route broker runner to be called")
	}
	if runner.plan.Address != "127.0.0.1:9443" {
		t.Fatalf("Address = %q", runner.plan.Address)
	}
	if runner.plan.CertFile != "/tmp/broker.crt" || runner.plan.KeyFile != "/tmp/broker.key" {
		t.Fatalf("plan = %#v", runner.plan)
	}
}

func TestAdminRouteBrokerServeRequiresConfiguredRunner(t *testing.T) {
	_, err := executeAdminForTest(t, "sandcastle-admin", "route-broker", "serve", "--cert", "/tmp/broker.crt", "--key", "/tmp/broker.key")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "route broker server") {
		t.Fatalf("error = %q", err)
	}
}

func TestConnectAliasCreatesMissingMachineBeforeConnecting(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	creator := &fakeMachineCreator{}
	connector := &fakeMachineConnector{}
	applier := &fakeDNSApplier{}
	knownHosts := &fakeKnownHostsManager{}
	stdout, stderr, err := executeForTestWithConfigAndStderr(t, commandConfig{
		name: "sandcastle",
		tenantStore: tenant.MemoryStore{Projects: []tenant.IncusProject{{
			Name:   "sc-acme",
			Config: configMap,
		}}},
		machineStore:     fakeMachineStatusStore{},
		machineCreator:   creator,
		machineConnector: connector,
		knownHosts:       knownHosts,
		dnsApplier:       applier,
	}, "c", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "Machine codex not found; creating it before connecting.") {
		t.Fatalf("stderr = %q", stderr)
	}
	if creator.plan.InstanceName != "default-codex" {
		t.Fatalf("created instance = %q", creator.plan.InstanceName)
	}
	if !applier.called || applier.tenant.Tenant != "acme" {
		t.Fatalf("expected DNS refresh for acme, got %#v", applier)
	}
	if !knownHosts.called || knownHosts.plan.InstanceName != "default-codex" {
		t.Fatalf("expected known_hosts refresh, got %#v", knownHosts.plan)
	}
	if !connector.called || connector.plan.InstanceName != "default-codex" || !connector.plan.Interactive {
		t.Fatalf("connector.plan = %#v", connector.plan)
	}
}

func TestRejectsUnknownOutputFormat(t *testing.T) {
	_, err := executeForTest(t, "sandcastle", "--output", "yaml", "version")
	if err == nil {
		t.Fatal("expected error")
	}
}

type fakeMachineCreator struct {
	plan machine.CreatePlan
}

func (f *fakeMachineCreator) CreateMachine(ctx context.Context, plan machine.CreatePlan) error {
	f.plan = plan
	return nil
}

type fakeKnownHostsManager struct {
	called bool
	plan   machine.CreatePlan
}

func (f *fakeKnownHostsManager) RefreshMachine(ctx context.Context, plan machine.CreatePlan) error {
	f.called = true
	f.plan = plan
	return nil
}

type fakeMachineConnector struct {
	called bool
	plan   machine.ConnectPlan
	err    error
}

func (f *fakeMachineConnector) ConnectMachine(ctx context.Context, plan machine.ConnectPlan, session machine.ConnectSession) error {
	f.called = true
	f.plan = plan
	return f.err
}

type fakeMachineController struct {
	called bool
	plan   machine.LifecyclePlan
}

func (f *fakeMachineController) ApplyLifecycle(ctx context.Context, plan machine.LifecyclePlan) error {
	f.called = true
	f.plan = plan
	return nil
}

type fakeTenantCreator struct {
	called bool
	plan   tenant.CreatePlan
}

func (f *fakeTenantCreator) CreateTenant(ctx context.Context, plan tenant.CreatePlan) error {
	f.called = true
	f.plan = plan
	return nil
}

type fakeProjectUpdater struct {
	called       bool
	incusProject string
	projects     []meta.Project
}

func (f *fakeProjectUpdater) SetTenantProjects(ctx context.Context, incusProjectName string, projects []meta.Project) error {
	f.called = true
	f.incusProject = incusProjectName
	f.projects = append([]meta.Project{}, projects...)
	return nil
}

type fakeSSHKeyUpdater struct {
	called       bool
	incusProject string
	key          string
}

func (f *fakeSSHKeyUpdater) SetTenantSSHKey(ctx context.Context, incusProjectName string, sshKey string) error {
	f.called = true
	f.incusProject = incusProjectName
	f.key = sshKey
	return nil
}

type fakeInfraCreator struct {
	called bool
	plan   infra.CreatePlan
}

func (f *fakeInfraCreator) CreateInfrastructure(ctx context.Context, plan infra.CreatePlan) error {
	f.called = true
	f.plan = plan
	return nil
}

func infraPlanHasFile(plan infra.CreatePlan, instance string, path string) bool {
	for _, file := range plan.RuntimeFiles {
		if file.Instance == instance && file.Path == path {
			return true
		}
	}
	return false
}

type fakeInfraDeleter struct {
	called bool
	plan   infra.DeletePlan
}

func (f *fakeInfraDeleter) DeleteInfrastructure(ctx context.Context, plan infra.DeletePlan) error {
	f.called = true
	f.plan = plan
	return nil
}

type fakeInfraCaddyDataExporter struct {
	called         bool
	plan           infra.CaddyDataExportPlan
	archiveContent []byte
}

func (f *fakeInfraCaddyDataExporter) ExportCaddyData(ctx context.Context, plan infra.CaddyDataExportPlan) (infra.CaddyDataExportResult, error) {
	f.called = true
	f.plan = plan
	if len(f.archiveContent) > 0 {
		if err := os.WriteFile(plan.ArchivePath, f.archiveContent, 0o600); err != nil {
			return infra.CaddyDataExportResult{}, err
		}
	}
	return infra.CaddyDataExportResult{
		Project:     plan.Project,
		Instance:    plan.Instance,
		SourcePath:  plan.SourcePath,
		ArchivePath: plan.ArchivePath,
	}, nil
}

type fakeImageManager struct {
	called bool
	plan   images.SyncPlan
	result images.SyncResult
}

func (f *fakeImageManager) SyncImage(ctx context.Context, plan images.SyncPlan) (images.SyncResult, error) {
	f.called = true
	f.plan = plan
	f.result.SyncPlan = plan
	return f.result, nil
}

type fakeImageBuilder struct {
	called bool
	plan   images.BuildPlan
	plans  []images.BuildPlan
}

func (f *fakeImageBuilder) BuildImage(ctx context.Context, plan images.BuildPlan) (images.BuildResult, error) {
	f.called = true
	f.plan = plan
	f.plans = append(f.plans, plan)
	return images.BuildResult{BuildPlan: plan, Built: true}, nil
}

type fakeImageImporter struct {
	called bool
	plan   images.ImportPlan
}

func (f *fakeImageImporter) ImportImage(ctx context.Context, plan images.ImportPlan) (images.ImportResult, error) {
	f.called = true
	f.plan = plan
	return images.ImportResult{ImportPlan: plan, Imported: true}, nil
}

type fakeImageUploader struct {
	called bool
	plan   images.UploadPlan
	plans  []images.UploadPlan
}

func (f *fakeImageUploader) UploadImage(ctx context.Context, plan images.UploadPlan) (images.UploadResult, error) {
	f.called = true
	f.plan = plan
	f.plans = append(f.plans, plan)
	return images.UploadResult{UploadPlan: plan, Uploaded: true}, nil
}

type fakeTailscaleRunner struct {
	called       bool
	statusCalled bool
	downCalled   bool
	plan         tailscale.UpPlan
	statusPlan   tailscale.StatusPlan
	downPlan     tailscale.DownPlan
	status       tailscale.StatusResult
}

type fakeLocalDNSManager struct {
	installed     bool
	refreshed     bool
	uninstalled   bool
	plan          localdns.Plan
	installPlan   localdns.Plan
	refreshPlan   localdns.Plan
	uninstallPlan localdns.Plan
}

type fakeDNSApplier struct {
	called bool
	tenant dns.Tenant
}

func (f *fakeLocalDNSManager) Install(ctx context.Context, plan localdns.Plan) (localdns.Result, error) {
	f.installed = true
	f.plan = plan
	f.installPlan = plan
	return localdns.Result{Reference: plan.Reference, Action: "install", StatePath: plan.StatePath, ResolverPath: plan.ResolverPath}, nil
}

func (f *fakeLocalDNSManager) Refresh(ctx context.Context, plan localdns.Plan) (localdns.Result, error) {
	f.refreshed = true
	f.plan = plan
	f.refreshPlan = plan
	return localdns.Result{Reference: plan.Reference, Action: "refresh", StatePath: plan.StatePath, ResolverPath: plan.ResolverPath}, nil
}

func (f *fakeLocalDNSManager) Uninstall(ctx context.Context, plan localdns.Plan) (localdns.Result, error) {
	f.uninstalled = true
	f.plan = plan
	f.uninstallPlan = plan
	return localdns.Result{Reference: plan.Reference, Action: "uninstall", StatePath: plan.StatePath, ResolverPath: plan.ResolverPath}, nil
}

func (f *fakeDNSApplier) Apply(ctx context.Context, tenant dns.Tenant) (dns.ApplyResult, error) {
	f.called = true
	f.tenant = tenant
	return dns.PlanApply(tenant, nil)
}

func (f *fakeTailscaleRunner) RunUp(ctx context.Context, plan tailscale.UpPlan, session tailscale.RunSession) error {
	f.called = true
	f.plan = plan
	return nil
}

func (f *fakeTailscaleRunner) RunStatus(ctx context.Context, plan tailscale.StatusPlan, session tailscale.RunSession) (tailscale.StatusResult, error) {
	f.statusCalled = true
	f.statusPlan = plan
	return f.status, nil
}

func (f *fakeTailscaleRunner) RunDown(ctx context.Context, plan tailscale.DownPlan, session tailscale.RunSession) error {
	f.downCalled = true
	f.downPlan = plan
	return nil
}

type fakeHostMachineStore struct{}

func (f fakeHostMachineStore) FindMachine(ctx context.Context, summary tenant.Summary, projectName string, name string) (meta.Machine, error) {
	return meta.Machine{
		Tenant:    summary.Tenant,
		Project:   projectName,
		Name:      name,
		AppPort:   3000,
		PrivateIP: "10.248.0.20",
		ExtraSANs: []string{"example.com"},
	}, nil
}

func (f fakeHostMachineStore) ListMachines(ctx context.Context, summary tenant.Summary) ([]meta.Machine, error) {
	machine, err := f.FindMachine(ctx, summary, "default", "codex")
	if err != nil {
		return nil, err
	}
	return []meta.Machine{machine}, nil
}

type fakeMachineStatusStore struct {
	machines  []meta.Machine
	unmanaged []machine.UnmanagedMachine
}

func (f fakeMachineStatusStore) ListMachines(ctx context.Context, summary tenant.Summary) ([]meta.Machine, error) {
	return f.machines, nil
}

func (f fakeMachineStatusStore) ListUnmanagedMachines(ctx context.Context, summary tenant.Summary) ([]machine.UnmanagedMachine, error) {
	return f.unmanaged, nil
}

type fakeHostOverrideManager struct {
	called  bool
	deleted bool
	plan    hostoverride.AddPlan
}

func (f *fakeHostOverrideManager) Add(ctx context.Context, plan hostoverride.AddPlan) error {
	f.called = true
	f.plan = plan
	return nil
}

func (f *fakeHostOverrideManager) Delete(ctx context.Context, plan hostoverride.DeletePlan) error {
	f.deleted = true
	return nil
}

type fakeRouteMachineStore struct{}

func (f fakeRouteMachineStore) FindMachine(ctx context.Context, summary tenant.Summary, projectName string, name string) (meta.Machine, error) {
	return meta.Machine{
		Tenant:    summary.Tenant,
		Project:   projectName,
		Name:      name,
		AppPort:   5173,
		PrivateIP: "10.248.0.20",
	}, nil
}

type fakeRouteManager struct {
	list route.ListResult
}

func (f *fakeRouteManager) Create(ctx context.Context, plan route.CreatePlan) error {
	return nil
}

func (f *fakeRouteManager) Delete(ctx context.Context, plan route.DeletePlan) error {
	return nil
}

func (f *fakeRouteManager) List(ctx context.Context, plan route.ListPlan) (route.ListResult, error) {
	return f.list, nil
}

type fakeRouteBrokerRunner struct {
	called bool
	plan   routebroker.ServePlan
}

func (f *fakeRouteBrokerRunner) Serve(ctx context.Context, plan routebroker.ServePlan) error {
	f.called = true
	f.plan = plan
	return nil
}

type fakeTrustManager struct {
	tokenCalled  bool
	grantCalled  bool
	revokeCalled bool
	deleteCalled bool
	usersCalled  bool
	plan         usertrust.UserPlan
	usersPlan    usertrust.TenantUsersPlan
	tenantUsers  usertrust.TenantUsersResult
	token        string
}

func (f *fakeTrustManager) Grant(ctx context.Context, plan usertrust.UserPlan) error {
	f.grantCalled = true
	f.plan = plan
	return nil
}

func (f *fakeTrustManager) Revoke(ctx context.Context, plan usertrust.UserPlan) error {
	f.revokeCalled = true
	f.plan = plan
	return nil
}

func (f *fakeTrustManager) Delete(ctx context.Context, plan usertrust.UserPlan) error {
	f.deleteCalled = true
	f.plan = plan
	return nil
}

func (f *fakeTrustManager) ListTenantUsers(ctx context.Context, plan usertrust.TenantUsersPlan) (usertrust.TenantUsersResult, error) {
	f.usersCalled = true
	f.usersPlan = plan
	if f.tenantUsers.Tenant == "" {
		return usertrust.TenantUsersResult{Tenant: plan.Tenant, IncusProject: plan.IncusProject}, nil
	}
	return f.tenantUsers, nil
}

func (f *fakeTrustManager) CreateToken(ctx context.Context, plan usertrust.UserPlan) (usertrust.TokenResult, error) {
	f.tokenCalled = true
	f.plan = plan
	return usertrust.TokenResult{
		User:            plan.User,
		CertificateName: plan.CertificateName,
		RemoteName:      plan.RemoteName,
		Restricted:      plan.Restricted,
		Projects:        plan.Projects,
		Token:           f.token,
	}, nil
}

type fakeHostFiles struct {
	called  bool
	deleted bool
	plan    hostoverride.AddPlan
}

func (f *fakeHostFiles) AddHostsEntry(ctx context.Context, plan hostoverride.AddPlan) error {
	f.called = true
	f.plan = plan
	return nil
}

func (f *fakeHostFiles) RemoveHostsEntry(ctx context.Context, plan hostoverride.DeletePlan) error {
	f.deleted = true
	return nil
}

type fakeLocalTrustManager struct {
	installed  bool
	deleted    bool
	plan       localtrust.Plan
	installErr error
}

func (f *fakeLocalTrustManager) Install(ctx context.Context, plan localtrust.Plan) (localtrust.Result, error) {
	f.installed = true
	f.plan = plan
	if f.installErr != nil {
		return localtrust.Result{}, f.installErr
	}
	return localtrust.Result{Reference: plan.Reference, TrustName: plan.TrustName, Action: "install", Platform: "fake"}, nil
}

func (f *fakeLocalTrustManager) Uninstall(ctx context.Context, plan localtrust.Plan) (localtrust.Result, error) {
	f.deleted = true
	f.plan = plan
	return localtrust.Result{Reference: plan.Reference, TrustName: plan.TrustName, Action: "uninstall", Platform: "fake"}, nil
}
