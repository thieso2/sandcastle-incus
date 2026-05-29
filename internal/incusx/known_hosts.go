package incusx

import (
	"path/filepath"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
)

func TenantKnownHostsPath(remote string, tenant string) string {
	return filepath.Join(config.DefaultConfigDir(), strings.TrimSpace(remote), "known_hosts", strings.TrimSpace(tenant))
}
