package incusx

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

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

	// Integrated ingress (see authapp_ingress.go): "none" (default, BYO edge),
	// "acme" (host :80/:443 + Let's Encrypt), or "cloudflare" (outbound tunnel,
	// no inbound ports; TunnelToken required).
	IngressMode string
	ACMEEmail   string
	TunnelToken string

	// Public Route ingress (Spec #111 coexistence): when "acme", the appliance
	// also binds host :80/:443 for native-ACME Public Route sites, independent of
	// IngressMode — so routes can run beside a Cloudflare-tunnelled login host.
	// RouteBaseDomain is where routes live (<label>.<tenant>.<base>); empty falls
	// back to the Auth Hostname.
	RouteIngress    string
	RouteBaseDomain string
	// RouteTLS overrides route-site TLS: "internal" = Caddy self-signed CA (for
	// hermetic e2e tests — no public DNS / ACME); empty = on-demand Let's Encrypt.
	RouteTLS string

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
	mode := strings.TrimSpace(req.IngressMode)
	units := []string{"sandcastle-auth-app.service"}
	if mode == IngressACME || mode == IngressCloudflare {
		ingressArch, err := applianceIngressArch(psrv, instance)
		if err != nil {
			return err
		}
		c.log("fetch ingress binaries on the host (caddy" + map[bool]string{true: " + cloudflared", false: ""}[mode == IngressCloudflare] + ", arch " + ingressArch + ")")
		caddy, cloudflared, err := fetchIngressBinaries(mode, ingressArch)
		if err != nil {
			return err
		}
		files = append(files,
			applianceFile{authAppCaddyBinary, caddy, 0o755},
			applianceFile{authAppCaddyfilePath, []byte(authAppCaddyfile(mode, req.Hostname, req.ACMEEmail)), 0o644},
			applianceFile{authAppCaddyUnitPath, []byte(authAppCaddyUnit()), 0o644},
		)
		units = append(units, "caddy.service")
		if mode == IngressCloudflare {
			if strings.TrimSpace(req.TunnelToken) == "" {
				return fmt.Errorf("cloudflare ingress requires a tunnel token")
			}
			files = append(files,
				applianceFile{authAppCloudflaredBinary, cloudflared, 0o755},
				applianceFile{authAppTunnelEnvPath, []byte("TUNNEL_TOKEN=" + strings.TrimSpace(req.TunnelToken) + "\n"), 0o600},
				applianceFile{authAppTunnelUnitPath, []byte(authAppTunnelUnit()), 0o644},
			)
			units = append(units, "cloudflared.service")
		}
	}
	for _, f := range files {
		if err := writeApplianceFile(psrv, instance, f); err != nil {
			return err
		}
	}

	c.log("start services: " + strings.Join(units, " "))
	start := applianceStartScript([]string{
		"install -d -m 0755 /etc/sandcastle",
		"install -d -m 0700 /etc/sandcastle/auth-app",
		"install -d -m 0700 /var/lib/sandcastle/auth",
	}, units...)
	if err := execSidecar(psrv, instance, start); err != nil {
		return fmt.Errorf("start auth-app services: %w", err)
	}
	switch mode {
	case IngressACME:
		c.log("done — auth-app " + instance + " terminating https://" + req.Hostname + " on host :80/:443 (Let's Encrypt)")
	case IngressCloudflare:
		c.log("done — auth-app " + instance + " serving https://" + req.Hostname + " via Cloudflare tunnel (no inbound ports)")
	default:
		c.log("done — auth-app " + instance + " on " + AuthAppListen +
			" (front it at https://" + req.Hostname + " via sc-edge)")
	}
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
	if existing, etag, err := server.GetInstance(instance); err == nil {
		// The appliance already exists (redeploy): keep it — and its auth DB in
		// the rootfs — but reconcile the ingress proxy devices, so a redeploy that
		// newly enables acme/route ingress binds host :80/:443 (Spec #111).
		return reconcileAuthAppIngressDevices(server, existing, etag, instance, req)
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
			Devices:  authAppDevices(req),
			Profiles: []string{},
		},
	})
	if err != nil {
		return fmt.Errorf("create auth-app appliance: %w", err)
	}
	if err := op.Wait(); err != nil && !isAlreadyRunning(err) {
		return fmt.Errorf("wait for auth-app appliance: %w", err)
	}
	return waitInstanceRunning(server, instance, 60*time.Second)
}

// applianceIngressArch resolves the caddy/cloudflared download-arch token
// ("amd64"/"arm64") from the running appliance instead of the admin host's
// runtime.GOARCH, so a darwin/arm64 admin installing onto an amd64 Incus host
// pushes ingress binaries that actually match the container.
func applianceIngressArch(server TenantResourceServer, instance string) (string, error) {
	inst, _, err := server.GetInstance(instance)
	if err != nil {
		return "", fmt.Errorf("read appliance %q architecture: %w", instance, err)
	}
	switch inst.Architecture {
	case "x86_64", "amd64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("appliance %q has unsupported architecture %q for ingress binaries", instance, inst.Architecture)
	}
}

// authAppDevices builds the appliance's device map; ACME ingress additionally
// publishes the host's :80/:443 into the container so caddy can terminate TLS
// and answer HTTP-01 challenges.
// reconcileAuthAppIngressDevices adds/removes the host :80/:443 proxy devices on
// an existing appliance to match the requested ingress, without recreating the
// container (the auth DB lives in its rootfs). Only the ingress devices are
// touched; root/eth0/incus-socket are left as-is.
func reconcileAuthAppIngressDevices(server TenantResourceServer, existing *api.Instance, etag, instance string, req BootstrapAuthAppRequest) error {
	want := authAppDevices(req)
	put := existing.Writable()
	next := api.DevicesMap{}
	for name, device := range put.Devices {
		next[name] = device
	}
	changed := false
	for _, name := range []string{"http", "https"} {
		desired, wanted := want[name]
		if wanted {
			if !reflect.DeepEqual(next[name], desired) {
				next[name] = desired
				changed = true
			}
		} else if _, present := next[name]; present {
			delete(next, name)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	put.Devices = next
	op, err := server.UpdateInstance(instance, put, etag)
	if err != nil {
		return fmt.Errorf("update auth-app ingress devices: %w", err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for auth-app ingress devices: %w", err)
	}
	return nil
}

func authAppDevices(req BootstrapAuthAppRequest) api.DevicesMap {
	devices := api.DevicesMap{
		"root": {"type": "disk", "pool": req.StoragePool, "path": "/"},
		"eth0": {"type": "nic", "nictype": "bridged", "parent": req.Bridge},
		// mount the host admin socket → the Auth App provisions with full rights
		"incus-socket": {"type": "disk", "source": AuthAppIncusSocket, "path": AuthAppIncusSocket},
	}
	// Bind the host :80/:443 for Caddy when the Auth Hostname uses ACME OR when
	// native-ACME Public Route ingress is enabled (routes need the ports even if
	// the login host is Cloudflare-tunnelled).
	if strings.TrimSpace(req.IngressMode) == IngressACME || strings.TrimSpace(req.RouteIngress) == IngressACME {
		devices["http"] = map[string]string{"type": "proxy", "listen": "tcp:0.0.0.0:80", "connect": "tcp:127.0.0.1:80"}
		devices["https"] = map[string]string{"type": "proxy", "listen": "tcp:0.0.0.0:443", "connect": "tcp:127.0.0.1:443"}
	}
	return devices
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
		// Public Routes (Spec #111): the running auth-app needs the Auth Hostname's
		// own ingress mode (to render its Caddy site), the ACME email, and — for
		// coexistence — whether native-ACME route ingress is on plus the route base
		// domain routes live under.
		"SANDCASTLE_AUTH_INGRESS_MODE=" + q(strings.TrimSpace(req.IngressMode)),
		"SANDCASTLE_AUTH_ACME_EMAIL=" + q(strings.TrimSpace(req.ACMEEmail)),
		"SANDCASTLE_ROUTE_INGRESS=" + q(strings.TrimSpace(req.RouteIngress)),
		"SANDCASTLE_ROUTE_BASE_DOMAIN=" + q(strings.TrimSpace(req.RouteBaseDomain)),
		"SANDCASTLE_ROUTE_TLS=" + q(strings.TrimSpace(req.RouteTLS)),
		// Incus access: the mounted host admin unix socket.
		"SANDCASTLE_REMOTE=" + q("local"),
		"SANDCASTLE_STORAGE_POOL=" + q(orDefaultStr(req.StoragePool, "default")),
		"SANDCASTLE_CIDR_POOL=" + q(orDefaultStr(req.CIDRPool, "10.248.0.0/16")),
		"SANDCASTLE_INCUS_PROJECT_PREFIX=" + q(orDefaultStr(req.ProjectPrefix, "sc")),
		"SANDCASTLE_INFRA_PROJECT=" + q(orDefaultStr(req.InfraProject, "sc-infra")),
		"SANDCASTLE_INFRA_TLS_MODE=" + q(orDefaultStr(req.TLSMode, "acme")),
		"SANDCASTLE_BASE_IMAGE=" + q(orDefaultStr(req.BaseImageRef, DefaultApplianceImage)),
		"SANDCASTLE_AI_IMAGE=" + q(orDefaultStr(req.AIImageRef, DefaultApplianceImage)),
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
