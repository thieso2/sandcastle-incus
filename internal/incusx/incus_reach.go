package incusx

import (
	"net"
	"strings"

	"github.com/lxc/incus/v6/shared/api"
)

// defaultIncusPort is the port Incus listens on when core.https_address names
// no port.
const defaultIncusPort = "8443"

// incusReachCommand forwards the sidecar's tailnet :8443 to the host Incus API
// on the tenant gateway (ADR-0017). Clients always dial the sidecar on 8443;
// only the host side follows the host's configured listener.
func incusReachCommand(gateway string, hostPort string) string {
	return "tailscale serve --bg --tcp=8443 tcp://" + net.JoinHostPort(gateway, hostPort)
}

type serverConfigReader interface {
	GetServer() (*api.Server, string, error)
}

// The admin paths hand hostIncusPort this wrapper, so it must pass the
// server config through.
var _ serverConfigReader = sdkTenantCreateServer{}

// hostIncusPort reads the port of the host's core.https_address. Hosts such as
// IncusOS installs that serve the API on :443 would otherwise get a Reach that
// forwards to a closed port. Servers that cannot report their config keep the
// default.
func hostIncusPort(server any) string {
	getter, ok := server.(serverConfigReader)
	if !ok {
		return defaultIncusPort
	}
	info, _, err := getter.GetServer()
	if err != nil || info == nil {
		return defaultIncusPort
	}
	return listenAddressPort(info.Config["core.https_address"])
}

func listenAddressPort(address string) string {
	address = strings.TrimSpace(address)
	if _, port, err := net.SplitHostPort(address); err == nil && port != "" {
		return port
	}
	return defaultIncusPort
}

func (s sdkTenantCreateServer) GetServer() (*api.Server, string, error) {
	return s.inner.GetServer()
}
