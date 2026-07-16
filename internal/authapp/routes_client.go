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
)

// Route client methods for the CLI (`sc route`). They drive the token-gated
// /api/routes endpoints with the CLI Auth Token, mirroring CreateProject.

// PublishRoute publishes a Route and returns the resulting view.
func (c DeviceClient) PublishRoute(ctx context.Context, request RoutePublishRequest) (RouteView, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return RouteView{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/api/routes"), bytes.NewReader(body))
	if err != nil {
		return RouteView{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.AuthToken))
	response, err := c.client().Do(httpRequest)
	if err != nil {
		return RouteView{}, err
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		return RouteView{}, fmt.Errorf("publish route: %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	var view RouteView
	if err := json.Unmarshal(payload, &view); err != nil {
		return RouteView{}, err
	}
	return view, nil
}

// ListRoutes returns the Routes published by a Tenant.
func (c DeviceClient) ListRoutes(ctx context.Context, tenant string) ([]RouteView, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/api/routes?tenant="+url.QueryEscape(strings.TrimSpace(tenant))), nil)
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.AuthToken))
	response, err := c.client().Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list routes: %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	var result RouteListResult
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, err
	}
	return result.Routes, nil
}

// GetRouteStatus returns one Route's live view.
func (c DeviceClient) GetRouteStatus(ctx context.Context, hostname string) (RouteView, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("/api/routes?hostname="+url.QueryEscape(strings.TrimSpace(hostname))), nil)
	if err != nil {
		return RouteView{}, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.AuthToken))
	response, err := c.client().Do(httpRequest)
	if err != nil {
		return RouteView{}, err
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		return RouteView{}, fmt.Errorf("route status: %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	var view RouteView
	if err := json.Unmarshal(payload, &view); err != nil {
		return RouteView{}, err
	}
	return view, nil
}

// DeleteRoute removes a Route by Hostname.
func (c DeviceClient) DeleteRoute(ctx context.Context, hostname string) error {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.url("/api/routes?hostname="+url.QueryEscape(strings.TrimSpace(hostname))), nil)
	if err != nil {
		return err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.AuthToken))
	response, err := c.client().Do(httpRequest)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("delete route: %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	return nil
}
