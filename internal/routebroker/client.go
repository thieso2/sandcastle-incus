package routebroker

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/route"
)

type Client struct {
	BaseURL            string
	CertFile           string
	KeyFile            string
	InsecureSkipVerify bool
	HTTPClient         *http.Client
}

type routeRequest struct {
	Hostname        string `json:"hostname"`
	TargetReference string `json:"targetReference"`
}

func (c Client) Add(ctx context.Context, plan route.AddPlan) error {
	client, baseURL, err := c.client()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(routeRequest{
		Hostname:        plan.Hostname,
		TargetReference: plan.TargetReference,
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/routes", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	return doRouteBrokerRequest(client, request, http.StatusCreated)
}

func (c Client) Remove(ctx context.Context, plan route.RemovePlan) error {
	client, baseURL, err := c.client()
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, baseURL+"/routes/"+url.PathEscape(plan.Hostname), nil)
	if err != nil {
		return err
	}
	return doRouteBrokerRequest(client, request, http.StatusOK)
}

func (c Client) List(ctx context.Context, plan route.ListPlan) (route.ListResult, error) {
	client, baseURL, err := c.client()
	if err != nil {
		return route.ListResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/routes", nil)
	if err != nil {
		return route.ListResult{}, err
	}
	response, err := doRouteBrokerRequestWithResponse(client, request, http.StatusOK)
	if err != nil {
		return route.ListResult{}, err
	}
	defer response.Body.Close()
	var result route.ListResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return route.ListResult{}, fmt.Errorf("decode route broker list response: %w", err)
	}
	return result, nil
}

func (c Client) client() (*http.Client, string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if baseURL == "" {
		return nil, "", fmt.Errorf("route broker URL is required")
	}
	if c.HTTPClient != nil {
		return c.HTTPClient, baseURL, nil
	}
	if strings.TrimSpace(c.CertFile) == "" || strings.TrimSpace(c.KeyFile) == "" {
		return nil, "", fmt.Errorf("route broker client certificate and key are required")
	}
	certificate, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, "", fmt.Errorf("load route broker client certificate: %w", err)
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		Certificates:       []tls.Certificate{certificate},
		InsecureSkipVerify: c.InsecureSkipVerify,
		MinVersion:         tls.VersionTLS12,
	}}}, baseURL, nil
}

func doRouteBrokerRequest(client *http.Client, request *http.Request, wantStatus int) error {
	response, err := doRouteBrokerRequestWithResponse(client, request, wantStatus)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	return nil
}

func doRouteBrokerRequestWithResponse(client *http.Client, request *http.Request, wantStatus int) (*http.Response, error) {
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == wantStatus {
		return response, nil
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	message := strings.TrimSpace(string(body))
	var payload map[string]string
	if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload["error"]) != "" {
		message = strings.TrimSpace(payload["error"])
	}
	return nil, fmt.Errorf("route broker %s %s: status %s: %s", request.Method, request.URL.Path, response.Status, message)
}
