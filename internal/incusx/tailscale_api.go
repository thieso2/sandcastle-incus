package incusx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// tailscaleAPIBase is the Tailscale control API. `-` selects the API key owner's
// default tailnet.
const tailscaleAPIBase = "https://api.tailscale.com/api/v2"

type tailscaleDevice struct {
	ID        string   `json:"id"`
	Addresses []string `json:"addresses"`
	Hostname  string   `json:"hostname"`
}

// ApproveTailscaleRoute approves (enables) an advertised subnet route on the
// tailnet device whose tailnet IP is sidecarTailnetIP, using a Tailscale API key
// (tskey-api-… or OAuth access token). It is optional route auto-approval: with no
// API key the caller skips it and the operator approves the route by hand.
//
// It also PRUNES stale duplicate sidecar devices: every re-provision of a tenant
// that re-registers the sidecar on the tailnet leaves the previous node behind
// (same hostname), and because each still advertises this tenant's unique /24, a
// subnet-router primary election can pick a dead one and blackhole all traffic to
// the tenant's machines. Deleting the same-hostname stragglers keeps exactly one
// approved router for the route. (Sidecar hostnames are tenant-unique, so a
// same-hostname device is always a prior incarnation of THIS sidecar — never
// another tenant.)
func ApproveTailscaleRoute(ctx context.Context, apiKey, sidecarTailnetIP, cidr string) error {
	apiKey = strings.TrimSpace(apiKey)
	sidecarTailnetIP = strings.TrimSpace(sidecarTailnetIP)
	cidr = strings.TrimSpace(cidr)
	if apiKey == "" || sidecarTailnetIP == "" || cidr == "" {
		return fmt.Errorf("tailscale route approval requires an API key, sidecar IP, and CIDR")
	}
	client := &http.Client{Timeout: 15 * time.Second}

	devices, err := tailscaleDevices(ctx, client, apiKey)
	if err != nil {
		return err
	}
	var current *tailscaleDevice
	for i := range devices {
		for _, a := range devices[i].Addresses {
			if a == sidecarTailnetIP {
				current = &devices[i]
			}
		}
	}
	if current == nil {
		return fmt.Errorf("no tailscale device found with address %s", sidecarTailnetIP)
	}

	// Prune stale prior incarnations of this sidecar (best-effort — a failed
	// delete must not block provisioning; the route approval below is what matters).
	if hostname := strings.TrimSpace(current.Hostname); hostname != "" {
		for i := range devices {
			if devices[i].ID == current.ID || !strings.EqualFold(devices[i].Hostname, hostname) {
				continue
			}
			_ = tailscaleDeleteDevice(ctx, client, apiKey, devices[i].ID)
		}
	}

	body, _ := json.Marshal(map[string][]string{"routes": {cidr}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		tailscaleAPIBase+"/device/"+current.ID+"/routes", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(apiKey, "")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("approve tailscale route: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("approve tailscale route: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return nil
}

func tailscaleDevices(ctx context.Context, client *http.Client, apiKey string) ([]tailscaleDevice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tailscaleAPIBase+"/tailnet/-/devices", nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(apiKey, "")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list tailscale devices: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("list tailscale devices: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var payload struct {
		Devices []tailscaleDevice `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Devices, nil
}

func tailscaleDeleteDevice(ctx context.Context, client *http.Client, apiKey, deviceID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, tailscaleAPIBase+"/device/"+deviceID, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(apiKey, "")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("delete tailscale device %s: %s: %s", deviceID, resp.Status, strings.TrimSpace(string(data)))
	}
	return nil
}
