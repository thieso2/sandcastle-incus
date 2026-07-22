package incusx

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/cliconfig"
)

// defaultRemoteDialTimeout bounds the pre-flight reachability probe below.
//
// The Incus client's connect takes no context and exposes no timeout knob, so
// an unreachable remote blocks it for ~20s with nothing on screen — which is
// indistinguishable from a hang, and is what every `sc` command does when the
// tailnet path to the host has gone away. A remote that is up answers the TCP
// handshake in milliseconds (direct) or a few hundred (DERP relayed), so five
// seconds is generous for "reachable" while still failing fast.
const defaultRemoteDialTimeout = 5 * time.Second

// remoteDialTimeout returns the probe budget, overridable for pathological
// networks (a relay-only path across an ocean) via SANDCASTLE_CONNECT_TIMEOUT.
// A zero or unparseable value disables the probe rather than failing the
// command — the escape hatch must never be the thing that breaks a connect.
func remoteDialTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("SANDCASTLE_CONNECT_TIMEOUT"))
	if raw == "" {
		return defaultRemoteDialTimeout
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

// connectConfiguredRemote loads the Incus CLI config at configPath, resolves
// remote (empty means the config's default remote) and connects to it.
//
// It is the one connect path for every non-admin Incus caller: the probe and
// the error wording belong in exactly one place, or the command that skipped
// them is the one that looks hung.
func connectConfiguredRemote(log func(string), configPath string, remote string) (incus.InstanceServer, error) {
	loaded, err := LoadCLIConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("load Incus config: %w", err)
	}
	if remote == "" {
		remote = loaded.DefaultRemote
	}
	if err := probeRemoteReachable(loaded, remote); err != nil {
		return nil, err
	}
	server, err := logIncusAPICall(log, "connect remote "+remote, func() (incus.InstanceServer, error) {
		return connectInstanceServer(loaded, remote)
	})
	if err != nil {
		return nil, fmt.Errorf("connect to Incus remote %q: %w", remote, err)
	}
	return server, nil
}

// probeRemoteReachable dials the remote's TCP endpoint before handing off to
// the Incus client. Anything it cannot meaningfully dial (a unix socket, an
// unparseable or unknown address) is passed through untouched so the client
// keeps ownership of those errors.
func probeRemoteReachable(loaded *cliconfig.Config, remote string) error {
	budget := remoteDialTimeout()
	if budget <= 0 {
		return nil
	}
	entry, ok := loaded.Remotes[remote]
	if !ok {
		return nil
	}
	address := remoteDialAddress(entry.Addr)
	if address == "" {
		return nil
	}
	conn, err := net.DialTimeout("tcp", address, budget)
	if err != nil {
		return fmt.Errorf("connect to Incus remote %q: %s is not reachable within %s (host down, or the network path to it — commonly Tailscale — is gone): %w",
			remote, address, budget, err)
	}
	return conn.Close()
}

// remoteDialAddress reduces a remote's configured addr to a host:port worth
// dialling, or "" when there is nothing to probe.
func remoteDialAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" || !strings.HasPrefix(addr, "https://") {
		return ""
	}
	parsed, err := url.Parse(addr)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	port := parsed.Port()
	if port == "" {
		// The Incus default port; the config normally spells it out.
		port = "8443"
	}
	return net.JoinHostPort(parsed.Hostname(), port)
}
