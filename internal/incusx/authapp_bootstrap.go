package incusx

import (
	"context"
	"fmt"
	"os"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"

	"github.com/thieso2/sandcastle-incus/internal/meta"
)

// Auth App appliance layout (mirrors the broker appliance). The Auth App serves
// plain HTTP on the bridge; a shared edge (sc-edge) terminates TLS for its
// public hostname and reverse-proxies to it, so the appliance needs NO host
// port — just a bridge NIC plus the mounted host admin socket for provisioning.
const (
	// DefaultApplianceImage is a stock systemd system-container base — no bespoke
	// Sandcastle image needed, since the self-contained static fat binary is
	// copied in and supplies everything. (Must be a system container, not OCI.)
	DefaultApplianceImage = "images:debian/13"

	AuthAppDefaultProject  = "infrastructure"
	AuthAppDefaultInstance = "sc2-auth-app"
	AuthAppBinaryPath      = "/usr/local/bin/sandcastle-admin"
	AuthAppEnvPath         = "/etc/sandcastle/auth-app/env"
	AuthAppUnitPath        = "/etc/systemd/system/sandcastle-auth-app.service"
	AuthAppDatabasePath    = "/var/lib/sandcastle/auth/auth.db"
	AuthAppListen          = ":9444"
	AuthAppIncusSocket     = "/var/lib/incus/unix.socket"
)

// BootstrapAuthAppRequest configures the one-time Auth App appliance deploy.
// Run on (or against) the Incus host: the appliance mounts the host's admin
// unix socket, so the Auth App provisions tenants over that socket with full
// rights (SANDCASTLE_REMOTE=local) — no TLS/remote/cert for its Incus side.
type BootstrapAuthAppRequest struct {
	Project     string // Incus project for the appliance (default "infrastructure")
	Instance    string // appliance instance name (default "sc2-auth-app")
	BaseImage   string // system-container base image (alias or fingerprint)
	BinaryPath  string // host path to the fat binary to push (default: this binary)
	Bridge      string // bridge the appliance NIC attaches to (e.g. incusbr0)
	StoragePool string // storage pool for the appliance root disk

	Hostname            string   // public Auth Hostname (e.g. sc2.thieso2.dev)
	GitHubClientID      string   // GitHub OAuth app client id
	GitHubClientSecret  string   // GitHub OAuth app client secret
	AdminGitHubUsers    []string // initial Sandcastle Admin GitHub usernames
	DefaultUnixUser     string   // default Unix login for provisioned machines
	TailscaleAuthKey    string   // key handed to approved device logins (optional)
	DebugDeviceUser     string   // enable debug device approval as this user (optional)
	SimulateGitHubToken string   // DEV ONLY: enable simulated-GitHub auth gated by this token (optional)

	// Provisioning config baked into the appliance env (the Auth App provisions
	// tenants on device login).
	CIDRPool      string
	ProjectPrefix string
	InfraProject  string
	TLSMode       string
	BaseImageRef  string // SANDCASTLE_BASE_IMAGE tenants are built from
	AIImageRef    string // SANDCASTLE_AI_IMAGE
}

// BootstrapAuthApp deploys the Auth App as an appliance and starts it.
func (c TenantCreator) BootstrapAuthApp(ctx context.Context, req BootstrapAuthAppRequest) error {
	server, err := c.resolveV2Server()
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.BinaryPath) == "" {
		return fmt.Errorf("binary path is required")
	}
	binary, err := os.ReadFile(req.BinaryPath)
	if err != nil {
		return fmt.Errorf("read binary %s: %w", req.BinaryPath, err)
	}
	project := orDefaultStr(req.Project, AuthAppDefaultProject)
	instance := orDefaultStr(req.Instance, AuthAppDefaultInstance)

	c.log("ensure auth-app project " + project)
	if err := ensureAuthAppProject(server, project); err != nil {
		return err
	}
	psrv := server.UseProject(project)

	c.log("launch auth-app appliance " + instance)
	if err := ensureAuthAppInstance(psrv, req, instance); err != nil {
		return err
	}

	c.log("install auth-app binary + config")
	files := []applianceFile{
		{AuthAppBinaryPath, binary, 0o755},
		{AuthAppEnvPath, []byte(authAppEnv(req)), 0o600},
		{AuthAppUnitPath, []byte(authAppUnit()), 0o644},
	}
	for _, f := range files {
		if err := writeApplianceFile(psrv, instance, f); err != nil {
			return err
		}
	}

	c.log("start auth-app service")
	start := applianceStartScript([]string{
		"install -d -m 0755 /etc/sandcastle",
		"install -d -m 0700 /etc/sandcastle/auth-app",
		"install -d -m 0700 /var/lib/sandcastle/auth",
	}, "sandcastle-auth-app.service")
	if err := execSidecar(psrv, instance, start); err != nil {
		return fmt.Errorf("start auth-app service: %w", err)
	}
	c.log("done — auth-app " + instance + " on " + AuthAppListen +
		" (front it at https://" + req.Hostname + " via sc-edge)")
	return nil
}

func ensureAuthAppProject(server TenantCreateServer, project string) error {
	if _, _, err := server.GetProject(project); err == nil {
		return nil
	}
	return server.CreateProject(api.ProjectsPost{
		Name: project,
		ProjectPut: api.ProjectPut{
			Description: "Sandcastle infrastructure",
			Config: api.ConfigMap{
				"features.networks": "false",
				"features.images":   "false",
				"features.profiles": "true",
			},
		},
	})
}

func ensureAuthAppInstance(server TenantResourceServer, req BootstrapAuthAppRequest, instance string) error {
	if _, _, err := server.GetInstance(instance); err == nil {
		return nil
	}
	source := imageInstanceSource(req.BaseImage)
	op, err := server.CreateInstance(api.InstancesPost{
		Name:   instance,
		Type:   "container",
		Start:  true,
		Source: source,
		InstancePut: api.InstancePut{
			Description: "Sandcastle Auth App",
			// Privileged so the container root == host root and can use the
			// mounted admin unix socket, matching the broker/auth-app pattern.
			Config: api.ConfigMap{
				"security.privileged": "true",
				meta.KeyKind:          "auth-app",
			},
			Devices: api.DevicesMap{
				"root": {"type": "disk", "pool": req.StoragePool, "path": "/"},
				"eth0": {"type": "nic", "nictype": "bridged", "parent": req.Bridge},
				// mount the host admin socket → the Auth App provisions with full rights
				"incus-socket": {"type": "disk", "source": AuthAppIncusSocket, "path": AuthAppIncusSocket},
				// No host proxy device: reached over the bridge via sc-edge.
			},
			Profiles: []string{},
		},
	})
	if err != nil {
		return fmt.Errorf("create auth-app appliance: %w", err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for auth-app appliance: %w", err)
	}
	return nil
}

type applianceFile struct {
	path    string
	content []byte
	mode    int
}

func writeApplianceFile(server TenantResourceServer, instance string, f applianceFile) error {
	if err := writeInstanceDir(server, instance, f.path); err != nil {
		return err
	}
	return server.CreateInstanceFile(instance, f.path, incus.InstanceFileArgs{
		Content:   strings.NewReader(string(f.content)),
		Type:      "file",
		Mode:      f.mode,
		WriteMode: "overwrite",
	})
}

func authAppEnv(req BootstrapAuthAppRequest) string {
	q := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "") + "'" }
	lines := []string{
		"SANDCASTLE_AUTH_LISTEN=" + q(AuthAppListen),
		"SANDCASTLE_AUTH_DB=" + q(AuthAppDatabasePath),
		"SANDCASTLE_AUTH_HOSTNAME=" + q(strings.TrimSpace(req.Hostname)),
		"SANDCASTLE_AUTH_GITHUB_CLIENT_ID=" + q(strings.TrimSpace(req.GitHubClientID)),
		"SANDCASTLE_AUTH_GITHUB_CLIENT_SECRET=" + q(strings.TrimSpace(req.GitHubClientSecret)),
		"SANDCASTLE_AUTH_ADMIN_GITHUB_USERS=" + q(strings.Join(req.AdminGitHubUsers, ",")),
		"SANDCASTLE_AUTH_DEBUG_DEVICE_USER=" + q(strings.TrimSpace(req.DebugDeviceUser)),
		"SANDCASTLE_AUTH_SIMULATE_GITHUB_TOKEN=" + q(strings.TrimSpace(req.SimulateGitHubToken)),
		"SANDCASTLE_AUTH_DEFAULT_UNIX_USER=" + q(strings.TrimSpace(req.DefaultUnixUser)),
		"SANDCASTLE_AUTH_TAILSCALE_AUTHKEY=" + q(strings.TrimSpace(req.TailscaleAuthKey)),
		// Incus access: the mounted host admin unix socket.
		"SANDCASTLE_REMOTE=" + q("local"),
		"SANDCASTLE_STORAGE_POOL=" + q(orDefaultStr(req.StoragePool, "default")),
		"SANDCASTLE_CIDR_POOL=" + q(orDefaultStr(req.CIDRPool, "10.248.0.0/16")),
		"SANDCASTLE_INCUS_PROJECT_PREFIX=" + q(orDefaultStr(req.ProjectPrefix, "sc")),
		"SANDCASTLE_INFRA_PROJECT=" + q(orDefaultStr(req.InfraProject, "sc-infra")),
		"SANDCASTLE_INFRA_TLS_MODE=" + q(orDefaultStr(req.TLSMode, "acme")),
		"SANDCASTLE_BASE_IMAGE=" + q(orDefaultStr(req.BaseImageRef, DefaultApplianceImage)),
		"SANDCASTLE_AI_IMAGE=" + q(orDefaultStr(req.AIImageRef, "sandcastle/ai:latest")),
	}
	return strings.Join(lines, "\n") + "\n"
}

func authAppUnit() string {
	return "[Unit]\nDescription=Sandcastle Auth App\nAfter=network-online.target\nWants=network-online.target\n\n" +
		"[Service]\nEnvironmentFile=" + AuthAppEnvPath + "\n" +
		"ExecStart=" + AuthAppBinaryPath + " auth-app serve" +
		" --listen ${SANDCASTLE_AUTH_LISTEN}" +
		" --database ${SANDCASTLE_AUTH_DB}" +
		" --auth-hostname ${SANDCASTLE_AUTH_HOSTNAME}" +
		" --github-client-id ${SANDCASTLE_AUTH_GITHUB_CLIENT_ID}" +
		" --github-client-secret ${SANDCASTLE_AUTH_GITHUB_CLIENT_SECRET}" +
		" --admin-github-users ${SANDCASTLE_AUTH_ADMIN_GITHUB_USERS}" +
		" --debug-device-user ${SANDCASTLE_AUTH_DEBUG_DEVICE_USER}" +
		" --simulate-github-token ${SANDCASTLE_AUTH_SIMULATE_GITHUB_TOKEN}" +
		" --default-unix-user ${SANDCASTLE_AUTH_DEFAULT_UNIX_USER}" +
		" --tailscale-auth-key ${SANDCASTLE_AUTH_TAILSCALE_AUTHKEY}\n" +
		"Restart=on-failure\n\n[Install]\nWantedBy=multi-user.target\n"
}

func orDefaultStr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
