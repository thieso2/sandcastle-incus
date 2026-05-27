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
)

const defaultDeviceClientTimeout = 2 * time.Minute

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
	body, _ := json.Marshal(map[string]string{
		"device_code":     deviceCode,
		"ssh_public_key":  poll.SSHPublicKey,
		"local_unix_user": poll.LocalUnixUser,
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
		Name:                              payload.Name,
		Provider:                          payload.Provider,
		GCPAudience:                       payload.GCPAudience,
		GCPSubjectTokenType:               payload.GCPSubjectTokenType,
		GCPServiceAccountImpersonationURL: payload.GCPServiceAccountImpersonationURL,
	}, nil
}

func (c DeviceClient) CreateShare(ctx context.Context, request ShareCreateRequest) (meta.TenantStorageShare, error) {
	body, _ := json.Marshal(request)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/api/shares"), bytes.NewReader(body))
	if err != nil {
		return meta.TenantStorageShare{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.AuthToken) != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.AuthToken))
	}
	response, err := c.client().Do(httpRequest)
	if err != nil {
		return meta.TenantStorageShare{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = response.Status
		}
		return meta.TenantStorageShare{}, fmt.Errorf("auth app share create: %s", msg)
	}
	var payload meta.TenantStorageShare
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return meta.TenantStorageShare{}, err
	}
	return payload, nil
}

func (c DeviceClient) ListShares(ctx context.Context, tenant string) ([]meta.TenantStorageShare, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/api/shares")+"?tenant="+url.QueryEscape(strings.TrimSpace(tenant)), nil)
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

func (c DeviceClient) GetShare(ctx context.Context, tenant string, project string, name string) (meta.TenantStorageShare, error) {
	query := "?tenant=" + url.QueryEscape(strings.TrimSpace(tenant)) + "&project=" + url.QueryEscape(strings.TrimSpace(project)) + "&name=" + url.QueryEscape(strings.TrimSpace(name))
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/api/shares/status")+query, nil)
	if err != nil {
		return meta.TenantStorageShare{}, err
	}
	if strings.TrimSpace(c.AuthToken) != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.AuthToken))
	}
	response, err := c.client().Do(httpRequest)
	if err != nil {
		return meta.TenantStorageShare{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = response.Status
		}
		return meta.TenantStorageShare{}, fmt.Errorf("auth app share status: %s", msg)
	}
	var payload meta.TenantStorageShare
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return meta.TenantStorageShare{}, err
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
