package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type loginTailnetVerifier interface {
	VerifyTenantTailnet(context.Context, string) (loginTailnetStatus, error)
}

type loginTailnetStatus struct {
	Tailnet string
	IPs     []string
}

type localLoginTailnetVerifier struct{}

func (localLoginTailnetVerifier) VerifyTenantTailnet(ctx context.Context, expectedTailnet string) (loginTailnetStatus, error) {
	expected := strings.TrimSpace(expectedTailnet)
	if expected == "" {
		return loginTailnetStatus{}, nil
	}
	output, err := exec.CommandContext(ctx, "tailscale", "status", "--json").Output()
	if err != nil {
		return loginTailnetStatus{}, fmt.Errorf("Tailscale Prerequisite failed: run tailscale up for Tenant Tailnet %s", expected)
	}
	return parseLocalTailscaleStatus(expected, output)
}

func parseLocalTailscaleStatus(expectedTailnet string, data []byte) (loginTailnetStatus, error) {
	var payload struct {
		BackendState   string `json:"BackendState"`
		CurrentTailnet struct {
			Name           string `json:"Name"`
			MagicDNSSuffix string `json:"MagicDNSSuffix"`
		} `json:"CurrentTailnet"`
		Self struct {
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Self"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return loginTailnetStatus{}, fmt.Errorf("parse local Tailscale status: %w", err)
	}
	state := strings.ToLower(strings.TrimSpace(payload.BackendState))
	if state != "running" {
		return loginTailnetStatus{}, fmt.Errorf("Tailscale Prerequisite failed: local Tailscale is %s; run tailscale up for Tenant Tailnet %s", payload.BackendState, expectedTailnet)
	}
	tailnet := payload.CurrentTailnet.Name
	if tailnet == "" {
		tailnet = payload.CurrentTailnet.MagicDNSSuffix
	}
	if tailnet != expectedTailnet {
		return loginTailnetStatus{}, fmt.Errorf("Tailscale Prerequisite failed: connected to tailnet %q, want %q", tailnet, expectedTailnet)
	}
	if len(payload.Self.TailscaleIPs) == 0 {
		return loginTailnetStatus{}, fmt.Errorf("Tailscale Prerequisite failed: no local Tailscale IP for Tenant Tailnet %s", expectedTailnet)
	}
	return loginTailnetStatus{Tailnet: tailnet, IPs: append([]string{}, payload.Self.TailscaleIPs...)}, nil
}
