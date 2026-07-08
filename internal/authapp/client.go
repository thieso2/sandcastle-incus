package authapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/projectbroker"
	"github.com/thieso2/sandcastle-incus/internal/share"
)

// defaultDeviceClientTimeout bounds each device poll. The poll that observes
// approval blocks while the server provisions the tenant, and first-login
// provisioning from a stock base image (download + install CoreDNS and
// Tailscale) can take a couple of minutes — a 2-minute budget lost that race by
// seconds. Keep it comfortably above the server's provisioning time so a single
// poll can await a first-login bring-up instead of erroring with a client
// timeout while the (detached) server work keeps running.
const defaultDeviceClientTimeout = 5 * time.Minute

type DeviceClient struct {
	BaseURL    string
	AuthToken  string
	HTTPClient *http.Client
}

type DeviceStartResult struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	ExpiresIn       int
	Interval        int
	Status          string
	Message         string
}

type DevicePollResult struct {
	Status             string
	Message            string
	UserKey            string
	CLIAuthToken       string
	Token              string
	RemoteName         string
	IncusRemoteAddress string
	IncusProject       string
	TailscaleLoginURL  string
	TenantPrivateCIDR  string
	AccessibleTenants  []string
	Projects           []string
	CurrentTenant      string
	CurrentProject     string
	SSHKeyFingerprint  string
	TenantTailnetState string
	TailscaleAuthKey   string
	NextCommand        string
	LoginResult        *CLILoginResult
	ExpiresIn          int
}

type DevicePollRequest struct {
	SSHPublicKey  string
	LocalUnixUser string
	// TailscaleAuthKey is the tenant's own tailnet key (BYO tailnet) used to
	// join the sidecar during provisioning; empty means interactive join.
	TailscaleAuthKey string
	// AwaitingTailnet re-runs the idempotent provisioning so an interactive
	// tailnet join is noticed (the client polls with this while waiting).
	AwaitingTailnet bool
	// DNSSuffix is the tenant-chosen Tenant DNS Suffix for first-login
	// provisioning (ADR-0018); empty means the tenant name.
	DNSSuffix string
	// ClientCertificatePEM is the client's existing shared-identity Incus
	// certificate, when one exists — the server unions this install's projects
	// into its trust entry (multi-install shared identity).
	ClientCertificatePEM string
}

func (c DeviceClient) Start(ctx context.Context) (DeviceStartResult, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/api/device/start"), nil)
	if err != nil {
		return DeviceStartResult{}, err
	}
	response, err := c.client().Do(request)
	if err != nil {
		return DeviceStartResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return DeviceStartResult{}, fmt.Errorf("auth app device start: %s", response.Status)
	}
	var payload deviceStartResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return DeviceStartResult{}, err
	}
	return DeviceStartResult{
		DeviceCode:      payload.DeviceCode,
		UserCode:        payload.UserCode,
		VerificationURI: payload.VerificationURI,
		ExpiresIn:       payload.ExpiresIn,
		Interval:        payload.Interval,
		Status:          payload.Status,
		Message:         payload.Message,
	}, nil
}

func (c DeviceClient) Poll(ctx context.Context, deviceCode string, poll DevicePollRequest) (DevicePollResult, error) {
	body, _ := json.Marshal(map[string]any{
		"device_code":        deviceCode,
		"ssh_public_key":     poll.SSHPublicKey,
		"local_unix_user":    poll.LocalUnixUser,
		"tailscale_auth_key": poll.TailscaleAuthKey,
		"awaiting_tailnet":   poll.AwaitingTailnet,
		"dns_suffix":         poll.DNSSuffix,
		"client_certificate": poll.ClientCertificatePEM,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/api/device/poll"), bytes.NewReader(body))
	if err != nil {
		return DevicePollResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client().Do(request)
	if err != nil {
		return DevicePollResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = response.Status
		}
		return DevicePollResult{}, fmt.Errorf("auth app device poll: %s", msg)
	}
	var payload devicePollResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return DevicePollResult{}, err
	}
	return DevicePollResult{
		Status:             payload.Status,
		Message:            payload.Message,
		UserKey:            payload.UserKey,
		CLIAuthToken:       payload.CLIAuthToken,
		Token:              payload.Token,
		RemoteName:         payload.RemoteName,
		IncusRemoteAddress: payload.IncusRemoteAddress,
		IncusProject:       payload.IncusProject,
		TailscaleLoginURL:  payload.TailscaleLoginURL,
		TenantPrivateCIDR:  payload.TenantPrivateCIDR,
		AccessibleTenants:  append([]string{}, payload.AccessibleTenants...),
		Projects:           append([]string{}, payload.Projects...),
		CurrentTenant:      payload.CurrentTenant,
		CurrentProject:     payload.CurrentProject,
		SSHKeyFingerprint:  payload.SSHKeyFingerprint,
		TenantTailnetState: payload.TenantTailnetState,
		TailscaleAuthKey:   payload.TailscaleAuthKey,
		NextCommand:        payload.NextCommand,
		LoginResult:        payload.LoginResult,
		ExpiresIn:          payload.ExpiresIn,
	}, nil
}

// DebugApprove calls the server-side /debug/device/approve endpoint, which
// auto-approves the pending device login without browser or GitHub interaction.
// The server must be running with --debug-device-user set to an allowlisted user.
func (c DeviceClient) DebugApprove(ctx context.Context, userCode string) error {
	body := strings.NewReader("user_code=" + userCode)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/debug/device/approve"), body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.client().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = response.Status
		}
		return fmt.Errorf("debug approve: %s", msg)
	}
	return nil
}

// CreateProject drives the token-gated POST /api/projects — the
// tunnel-friendly tenant plane for project creation (no broker port).
func (c DeviceClient) CreateProject(ctx context.Context, project string) (projectbroker.ProjectResult, error) {
	body, _ := json.Marshal(map[string]string{"project": project})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/api/projects"), bytes.NewReader(body))
	if err != nil {
		return projectbroker.ProjectResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.AuthToken))
	response, err := c.client().Do(request)
	if err != nil {
		return projectbroker.ProjectResult{}, err
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		return projectbroker.ProjectResult{}, fmt.Errorf("create project: %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	var result projectbroker.ProjectResult
	if err := json.Unmarshal(payload, &result); err != nil {
		return projectbroker.ProjectResult{}, err
	}
	return result, nil
}

// SimulateApprove drives the token-gated /oauth/github/simulate endpoint to
// approve the pending device login as `username`, with no browser and no GitHub.
// The server must be running with --simulate-github-token equal to `token`. This
// is the offline counterpart to a real GitHub device login (DEV ONLY).
func (c DeviceClient) SimulateApprove(ctx context.Context, userCode, username, token string) error {
	form := url.Values{}
	form.Set("token", token)
	form.Set("username", username)
	form.Set("user_code", userCode)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/oauth/github/simulate"), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.client().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = response.Status
		}
		return fmt.Errorf("simulate approve: %s", msg)
	}
	return nil
}

func (c DeviceClient) EnableWorkload(ctx context.Context, request WorkloadEnableRequest) (WorkloadEnableResult, error) {
	body, _ := json.Marshal(request)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/api/workload/enable"), bytes.NewReader(body))
	if err != nil {
		return WorkloadEnableResult{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.AuthToken) != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.AuthToken))
	}
	response, err := c.client().Do(httpRequest)
	if err != nil {
		return WorkloadEnableResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = response.Status
		}
		return WorkloadEnableResult{}, fmt.Errorf("auth app workload enable: %s", msg)
	}
	var payload WorkloadEnableResult
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return WorkloadEnableResult{}, err
	}
	return payload, nil
}

func (c DeviceClient) UpsertCloudIdentity(ctx context.Context, request CloudIdentityUpsertRequest) (CloudIdentityConfig, error) {
	body, _ := json.Marshal(request)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/api/cloud-identities"), bytes.NewReader(body))
	if err != nil {
		return CloudIdentityConfig{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.AuthToken) != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.AuthToken))
	}
	response, err := c.client().Do(httpRequest)
	if err != nil {
		return CloudIdentityConfig{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = response.Status
		}
		return CloudIdentityConfig{}, fmt.Errorf("auth app cloud identity upsert: %s", msg)
	}
	var payload struct {
		ID                                string `json:"id"`
		UserKey                           string `json:"user_key"`
		Tenant                            string `json:"tenant"`
		Name                              string `json:"name"`
		Provider                          string `json:"provider"`
		GCPAudience                       string `json:"gcp_audience"`
		GCPSubjectTokenType               string `json:"gcp_subject_token_type"`
		GCPServiceAccountImpersonationURL string `json:"gcp_service_account_impersonation_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return CloudIdentityConfig{}, err
	}
	return CloudIdentityConfig{
		ID:                                payload.ID,
		UserKey:                           payload.UserKey,
		Tenant:                            payload.Tenant,
		Name:                              payload.Name,
		Provider:                          payload.Provider,
		GCPAudience:                       payload.GCPAudience,
		GCPSubjectTokenType:               payload.GCPSubjectTokenType,
		GCPServiceAccountImpersonationURL: payload.GCPServiceAccountImpersonationURL,
	}, nil
}

func (c DeviceClient) GetCloudIdentity(ctx context.Context, tenant string, name string) (CloudIdentityConfig, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/api/cloud-identities"), nil)
	if err != nil {
		return CloudIdentityConfig{}, err
	}
	query := httpRequest.URL.Query()
	query.Set("tenant", strings.TrimSpace(tenant))
	query.Set("name", strings.TrimSpace(name))
	httpRequest.URL.RawQuery = query.Encode()
	if strings.TrimSpace(c.AuthToken) != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.AuthToken))
	}
	response, err := c.client().Do(httpRequest)
	if err != nil {
		return CloudIdentityConfig{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = response.Status
		}
		return CloudIdentityConfig{}, fmt.Errorf("auth app cloud identity get: %s", msg)
	}
	var payload struct {
		ID                                string `json:"id"`
		UserKey                           string `json:"user_key"`
		Tenant                            string `json:"tenant"`
		Name                              string `json:"name"`
		Provider                          string `json:"provider"`
		GCPAudience                       string `json:"gcp_audience"`
		GCPSubjectTokenType               string `json:"gcp_subject_token_type"`
		GCPServiceAccountImpersonationURL string `json:"gcp_service_account_impersonation_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return CloudIdentityConfig{}, err
	}
	return CloudIdentityConfig{
		ID:                                payload.ID,
		UserKey:                           payload.UserKey,
		Tenant:                            payload.Tenant,
		Name:                              payload.Name,
		Provider:                          payload.Provider,
		GCPAudience:                       payload.GCPAudience,
		GCPSubjectTokenType:               payload.GCPSubjectTokenType,
		GCPServiceAccountImpersonationURL: payload.GCPServiceAccountImpersonationURL,
	}, nil
}

func (c DeviceClient) ListTenants(ctx context.Context) ([]TenantAccessSummary, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/api/tenants"), nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.AuthToken) != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.AuthToken))
	}
	response, err := c.client().Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = response.Status
		}
		return nil, fmt.Errorf("auth app tenant list: %s", msg)
	}
	var payload TenantAccessListResult
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Tenants, nil
}

func (c DeviceClient) CreateShare(ctx context.Context, request ShareCreateRequest) (share.Result, error) {
	body, _ := json.Marshal(request)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/api/shares"), bytes.NewReader(body))
	if err != nil {
		return share.Result{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.AuthToken) != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.AuthToken))
	}
	response, err := c.client().Do(httpRequest)
	if err != nil {
		return share.Result{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = response.Status
		}
		return share.Result{}, fmt.Errorf("auth app share create: %s", msg)
	}
	var payload share.Result
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return share.Result{}, err
	}
	return payload, nil
}

func (c DeviceClient) ListShares(ctx context.Context, tenant string) ([]meta.TenantStorageShare, error) {
	return c.listShares(ctx, tenant, "")
}

func (c DeviceClient) ListInboundShares(ctx context.Context, tenant string) ([]meta.TenantStorageShare, error) {
	return c.listShares(ctx, tenant, "inbound")
}

func (c DeviceClient) ListShareOffers(ctx context.Context, tenant string) ([]meta.TenantStorageShare, error) {
	return c.listShares(ctx, tenant, "offers")
}

func (c DeviceClient) listShares(ctx context.Context, tenant string, direction string) ([]meta.TenantStorageShare, error) {
	query := "?tenant=" + url.QueryEscape(strings.TrimSpace(tenant))
	if strings.TrimSpace(direction) != "" {
		query += "&direction=" + url.QueryEscape(strings.TrimSpace(direction))
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/api/shares")+query, nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.AuthToken) != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.AuthToken))
	}
	response, err := c.client().Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = response.Status
		}
		return nil, fmt.Errorf("auth app share list: %s", msg)
	}
	var payload ShareListResult
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Shares, nil
}

func (c DeviceClient) GetShare(ctx context.Context, request ShareStatusRequest) (share.Result, error) {
	query := "?tenant=" + url.QueryEscape(strings.TrimSpace(request.Tenant)) + "&project=" + url.QueryEscape(strings.TrimSpace(request.Project)) + "&name=" + url.QueryEscape(strings.TrimSpace(request.Name))
	if strings.TrimSpace(request.SourceTenant) != "" {
		query += "&source_tenant=" + url.QueryEscape(strings.TrimSpace(request.SourceTenant))
	}
	if request.Inbound {
		query += "&direction=inbound"
	}
	if request.Verbose {
		query += "&verbose=1"
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/api/shares/status")+query, nil)
	if err != nil {
		return share.Result{}, err
	}
	if strings.TrimSpace(c.AuthToken) != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.AuthToken))
	}
	response, err := c.client().Do(httpRequest)
	if err != nil {
		return share.Result{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = response.Status
		}
		return share.Result{}, fmt.Errorf("auth app share status: %s", msg)
	}
	var payload share.Result
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return share.Result{}, err
	}
	return payload, nil
}

func (c DeviceClient) AcceptShare(ctx context.Context, request ShareRecipientRequest) (share.Result, error) {
	return c.shareRecipientMutation(ctx, "/api/shares/accept", request, "accept")
}

func (c DeviceClient) DeclineShare(ctx context.Context, request ShareRecipientRequest) (share.Result, error) {
	return c.shareRecipientMutation(ctx, "/api/shares/decline", request, "decline")
}

func (c DeviceClient) RevokeShare(ctx context.Context, request ShareRevokeRequest) (share.Result, error) {
	body, _ := json.Marshal(request)
	return c.postShareResult(ctx, "/api/shares/revoke", body, "revoke")
}

func (c DeviceClient) DeleteShare(ctx context.Context, request ShareDeleteRequest) (share.Result, error) {
	body, _ := json.Marshal(request)
	return c.postShareResult(ctx, "/api/shares/delete", body, "delete")
}

func (c DeviceClient) shareRecipientMutation(ctx context.Context, path string, request ShareRecipientRequest, label string) (share.Result, error) {
	body, _ := json.Marshal(request)
	return c.postShareResult(ctx, path, body, label)
}

func (c DeviceClient) postShareResult(ctx context.Context, path string, body []byte, label string) (share.Result, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(path), bytes.NewReader(body))
	if err != nil {
		return share.Result{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.AuthToken) != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.AuthToken))
	}
	response, err := c.client().Do(httpRequest)
	if err != nil {
		return share.Result{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = response.Status
		}
		return share.Result{}, fmt.Errorf("auth app share %s: %s", label, msg)
	}
	var payload share.Result
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return share.Result{}, err
	}
	return payload, nil
}

func (c DeviceClient) ReconcileShares(ctx context.Context, request ShareReconcileRequest) (share.ReconcileResult, error) {
	body, _ := json.Marshal(request)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/api/shares/reconcile"), bytes.NewReader(body))
	if err != nil {
		return share.ReconcileResult{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.AuthToken) != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.AuthToken))
	}
	response, err := c.client().Do(httpRequest)
	if err != nil {
		return share.ReconcileResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = response.Status
		}
		return share.ReconcileResult{}, fmt.Errorf("auth app share reconcile: %s", msg)
	}
	var payload share.ReconcileResult
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return share.ReconcileResult{}, err
	}
	return payload, nil
}

func (c DeviceClient) url(path string) string {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}
	return base + path
}

func (c DeviceClient) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: defaultDeviceClientTimeout}
}
