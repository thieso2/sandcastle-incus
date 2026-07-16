package authapp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// LocalCaddyController is the production CaddyController: the auth-app runs in the
// same container as Caddy, so it writes the appliance Caddyfile to the local
// filesystem and reloads Caddy in place. No Incus involved.
type LocalCaddyController struct {
	// CaddyfilePath is the on-disk Caddyfile; defaults to /etc/caddy/Caddyfile.
	CaddyfilePath string
	// CaddyBinary is the caddy executable; defaults to /usr/bin/caddy.
	CaddyBinary string
}

// DefaultCaddyfilePath and DefaultCaddyBinary mirror the appliance layout
// (internal/incusx/authapp_ingress.go).
const (
	DefaultCaddyfilePath = "/etc/caddy/Caddyfile"
	DefaultCaddyBinary   = "/usr/bin/caddy"
)

func (c LocalCaddyController) Apply(ctx context.Context, caddyfile string) error {
	path := c.CaddyfilePath
	if path == "" {
		path = DefaultCaddyfilePath
	}
	binary := c.CaddyBinary
	if binary == "" {
		binary = DefaultCaddyBinary
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("prepare caddy config dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(caddyfile), 0o644); err != nil {
		return fmt.Errorf("write caddyfile: %w", err)
	}
	// Reload the running Caddy against the new file. `caddy reload` talks to
	// Caddy's admin API directly, so it does not depend on systemd being the
	// supervisor.
	cmd := exec.CommandContext(ctx, binary, "reload", "--config", path, "--force")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("caddy reload: %w: %s", err, string(output))
	}
	return nil
}
