package incusx

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// Integrated ingress: the auth-app appliance can terminate its own public
// hostname instead of relying on a separate sc-edge appliance. Two modes:
//
//   - "acme":       caddy binds the HOST's :80/:443 (proxy devices) and
//     terminates TLS with a real Let's Encrypt certificate;
//     requires a public IP + an A record for the hostname.
//   - "cloudflare": cloudflared dials OUT to a Cloudflare tunnel and caddy
//     serves plain HTTP on :8080 (Cloudflare terminates TLS at
//     the edge); no inbound ports at all.
//
// Both proxy to the auth-app on 127.0.0.1:9444 inside the same container, so
// there is no cross-container IP wiring, and the auth-app service can later
// manage the Caddyfile itself (tenant public routes).
const (
	IngressNone       = "none"
	IngressACME       = "acme"
	IngressCloudflare = "cloudflare"

	authAppCaddyBinary       = "/usr/bin/caddy"
	authAppCloudflaredBinary = "/usr/bin/cloudflared"
	authAppCaddyfilePath     = "/etc/caddy/Caddyfile"
	authAppCaddyUnitPath     = "/etc/systemd/system/caddy.service"
	authAppTunnelUnitPath    = "/etc/systemd/system/cloudflared.service"
	authAppTunnelEnvPath     = "/etc/default/cloudflared"
)

// authAppCaddyfile renders the appliance Caddyfile for the ingress mode.
func authAppCaddyfile(mode string, hostname string, acmeEmail string) string {
	hostname = strings.TrimSpace(hostname)
	switch mode {
	case IngressACME:
		email := ""
		if strings.TrimSpace(acmeEmail) != "" {
			email = "\temail " + strings.TrimSpace(acmeEmail) + "\n"
		}
		return "{\n" + email + "}\n\n" +
			hostname + " {\n\treverse_proxy 127.0.0.1:9444\n}\n"
	case IngressCloudflare:
		return "{\n\tauto_https off\n}\n\n" +
			"http://" + hostname + ":8080 {\n\treverse_proxy 127.0.0.1:9444\n}\n"
	default:
		return ""
	}
}

func authAppCaddyUnit() string {
	return `[Unit]
Description=Caddy (sandcastle auth-app ingress)
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
ExecStart=/usr/bin/caddy run --config /etc/caddy/Caddyfile
ExecReload=/usr/bin/caddy reload --config /etc/caddy/Caddyfile --force
TimeoutStopSec=5s
LimitNOFILE=1048576
Restart=on-failure
RestartSec=2s

[Install]
WantedBy=multi-user.target
`
}

func authAppTunnelUnit() string {
	return `[Unit]
Description=cloudflared (Cloudflare Tunnel for the sandcastle auth-app)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/default/cloudflared
ExecStart=/usr/bin/cloudflared tunnel --no-autoupdate run
TimeoutStopSec=5s
Restart=on-failure
RestartSec=2s

[Install]
WantedBy=multi-user.target
`
}

// fetchIngressBinaries downloads caddy (and cloudflared for tunnel mode) ON THE
// HOST — an in-container download over the NAT'd bridge can crawl or time out,
// while the host fetch takes seconds (hard-won sc-edge lesson).
func fetchIngressBinaries(mode string) (caddy []byte, cloudflared []byte, err error) {
	arch := runtime.GOARCH
	caddy, err = downloadURL("https://caddyserver.com/api/download?os=linux&arch="+arch, 3*time.Minute)
	if err != nil {
		return nil, nil, fmt.Errorf("download caddy: %w", err)
	}
	if mode == IngressCloudflare {
		cloudflared, err = downloadURL("https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-"+arch, 3*time.Minute)
		if err != nil {
			return nil, nil, fmt.Errorf("download cloudflared: %w", err)
		}
	}
	return caddy, cloudflared, nil
}

func downloadURL(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	response, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, response.Status)
	}
	return io.ReadAll(response.Body)
}
