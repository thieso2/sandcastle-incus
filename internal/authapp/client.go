package authapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type DeviceClient struct {
	BaseURL    string
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
	Token              string
	RemoteName         string
	AccessibleTenants  []string
	Projects           []string
	CurrentTenant      string
	CurrentProject     string
	SSHKeyFingerprint  string
	TenantTailnetState string
	NextCommand        string
	LoginResult        *CLILoginResult
	ExpiresIn          int
}

type DevicePollRequest struct {
	SSHPublicKey string
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
		"device_code":    deviceCode,
		"ssh_public_key": poll.SSHPublicKey,
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
		return DevicePollResult{}, fmt.Errorf("auth app device poll: %s", response.Status)
	}
	var payload devicePollResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return DevicePollResult{}, err
	}
	return DevicePollResult{
		Status:             payload.Status,
		Message:            payload.Message,
		UserKey:            payload.UserKey,
		Token:              payload.Token,
		RemoteName:         payload.RemoteName,
		AccessibleTenants:  append([]string{}, payload.AccessibleTenants...),
		Projects:           append([]string{}, payload.Projects...),
		CurrentTenant:      payload.CurrentTenant,
		CurrentProject:     payload.CurrentProject,
		SSHKeyFingerprint:  payload.SSHKeyFingerprint,
		TenantTailnetState: payload.TenantTailnetState,
		NextCommand:        payload.NextCommand,
		LoginResult:        payload.LoginResult,
		ExpiresIn:          payload.ExpiresIn,
	}, nil
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
	return &http.Client{Timeout: 10 * time.Second}
}
