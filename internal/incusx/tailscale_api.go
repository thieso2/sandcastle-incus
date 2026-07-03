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

// ApproveTailscaleRoute approves (enables) an advertised subnet route on the
// tailnet device whose tailnet IP is sidecarTailnetIP, using a Tailscale API key
// (tskey-api-… or OAuth access token). It is optional route auto-approval: with no
// API key the caller skips it and the operator approves the route by hand.
func ApproveTailscaleRoute(ctx context.Context, apiKey, sidecarTailnetIP, cidr string) error {
	apiKey = strings.TrimSpace(apiKey)
	sidecarTailnetIP = strings.TrimSpace(sidecarTailnetIP)
	cidr = strings.TrimSpace(cidr)
	if apiKey == "" || sidecarTailnetIP == "" || cidr == "" {
		return fmt.Errorf("tailscale route approval requires an API key, sidecar IP, and CIDR")
	}
	client := &http.Client{Timeout: 15 * time.Second}

	deviceID, err := tailscaleDeviceIDByAddress(ctx, client, apiKey, sidecarTailnetIP)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string][]string{"routes": {cidr}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		tailscaleAPIBase+"/device/"+deviceID+"/routes", bytes.NewReader(body))
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

func tailscaleDeviceIDByAddress(ctx context.Context, client *http.Client, apiKey, address string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tailscaleAPIBase+"/tailnet/-/devices", nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(apiKey, "")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("list tailscale devices: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("list tailscale devices: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var payload struct {
		Devices []struct {
			ID        string   `json:"id"`
			Addresses []string `json:"addresses"`
		} `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	for _, d := range payload.Devices {
		for _, a := range d.Addresses {
			if a == address {
				return d.ID, nil
			}
		}
	}
	return "", fmt.Errorf("no tailscale device found with address %s", address)
}
