package incusx

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	cliconfig "github.com/lxc/incus/v6/shared/cliconfig"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

// CreateV2Options carries per-run inputs that must not live in the persisted
// plan — chiefly the tenant's Tailscale auth key (a secret) and the sidecar
// image override (must be a *system-container* base image; the raw OCI base
// launches as an application container with no systemd).
type CreateV2Options struct {
	TailscaleAuthKey string
	SidecarImage     string
	// OnTailscaleLoginURL, when set, is invoked with the sidecar's interactive
	// `tailscale up` login URL when no auth key was supplied. The caller shows it
	// to the user, who visits it to register the sidecar into their tailnet.
	OnTailscaleLoginURL func(url string)
	// OnSidecarTailnetIP, when set, is invoked with the sidecar's tailnet IPv4
	// once it joins (auth-key path only). The client's Incus remote is pointed at
	// this address:8443 — the sidecar proxies it to the host's Incus (ADR-0017).
	OnSidecarTailnetIP func(ip string)
	// TailscaleAPIKey, when set, enables optional route auto-approval: after the
	// sidecar advertises the tenant's private CIDR, the route is approved via the
	// Tailscale API so clients can reach tenant machines without a manual admin
	// step. Empty = leave the route pending manual approval.
	TailscaleAPIKey string
}

// CreateTenantV2 executes a CreatePlanV2 against Incus, reproducing the v2 MVP
// topology proven manually on `big` (see PROGRESS.md): a shared per-tenant
// bridge in the default project, an infra project holding one sidecar
// (CoreDNS + Tailscale subnet-router), and a seeded default app project whose
// default profile carries the shared bridge NIC + cloud-init login.
func (c TenantCreator) CreateTenantV2(ctx context.Context, plan tenant.CreatePlanV2, opts CreateV2Options) error {
	server, err := c.resolveV2Server()
	if err != nil {
		return err
	}
	sidecarImage := strings.TrimSpace(opts.SidecarImage)
	if sidecarImage == "" {
		sidecarImage = plan.SidecarImage
	}

	c.log("ensure shared bridge " + plan.Bridge + " (in default)")
	if err := ensureV2Bridge(server, plan); err != nil {
		return err
	}
	c.log("ensure infra project " + plan.InfraProject)
	if err := ensureV2Project(server, plan.InfraProject, "Sandcastle v2 infra for "+plan.Tenant, "infra", plan.Tenant, false, v2InfraMetadata(plan)); err != nil {
		return err
	}
	c.log("ensure app project " + plan.DefaultProject)
	if err := ensureV2Project(server, plan.DefaultProject, "Sandcastle v2 project default for "+plan.Tenant, "project", plan.Tenant, true, nil); err != nil {
		return err
	}
	c.log("ensure app default profile " + plan.DefaultProject)
	if err := ensureV2AppProfile(server.UseProject(plan.DefaultProject), plan); err != nil {
		return err
	}
	c.log("ensure sidecar profile")
	if err := ensureV2SidecarProfile(server.UseProject(plan.InfraProject), plan); err != nil {
		return err
	}
	c.log("launch sidecar " + plan.SidecarInstance + " (image " + sidecarImage + ")")
	if err := ensureV2Sidecar(server.UseProject(plan.InfraProject), plan, sidecarImage); err != nil {
		return err
	}
	c.log("install CoreDNS + Tailscale on sidecar (stock base image)")
	if err := installV2SidecarPackages(server.UseProject(plan.InfraProject), plan); err != nil {
		return err
	}
	c.log("configure sidecar network + CoreDNS")
	if err := configureV2Sidecar(server.UseProject(plan.InfraProject), plan); err != nil {
		return err
	}
	c.log("tailscale up (advertise " + plan.PrivateCIDR + ")")
	loginURL, sidecarIP, err := v2TailscaleUp(server.UseProject(plan.InfraProject), plan, strings.TrimSpace(opts.TailscaleAuthKey))
	if err != nil {
		return err
	}
	if loginURL != "" {
		c.log("tailscale: sidecar not on a tailnet yet — register it at " + loginURL)
		if opts.OnTailscaleLoginURL != nil {
			opts.OnTailscaleLoginURL(loginURL)
		}
	}
	if sidecarIP != "" {
		c.log("sidecar tailnet IP " + sidecarIP + " (Incus Reach)")
		if opts.OnSidecarTailnetIP != nil {
			opts.OnSidecarTailnetIP(sidecarIP)
		}
		// Optional route auto-approval: approve the sidecar's advertised tenant
		// CIDR so clients reach tenant machines without a manual admin step.
		if strings.TrimSpace(opts.TailscaleAPIKey) != "" {
			c.log("approving tenant route " + plan.PrivateCIDR + " via Tailscale API")
			if err := ApproveTailscaleRoute(ctx, opts.TailscaleAPIKey, sidecarIP, plan.PrivateCIDR); err != nil {
				c.log("WARNING: tenant route auto-approval failed (approve it manually): " + err.Error())
			}
		}
	}
	c.log("done")
	return nil
}

func (c TenantCreator) resolveV2Server() (TenantCreateServer, error) {
	if c.Server != nil {
		return c.Server, nil
	}
	loaded, err := cliconfig.LoadConfig(c.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load Incus config: %w", err)
	}
	remote := c.Remote
	if remote == "" {
		remote = loaded.DefaultRemote
	}
	instanceServer, err := loaded.GetInstanceServer(remote)
	if err != nil {
		return nil, fmt.Errorf("connect to Incus remote %q: %w", remote, err)
	}
	return sdkTenantCreateServer{inner: instanceServer}, nil
}

// ensureV2Bridge creates the shared per-tenant bridge in the default project.
func ensureV2Bridge(server TenantCreateServer, plan tenant.CreatePlanV2) error {
	def := server.UseProject(naming.DefaultProjectName)
	if _, _, err := def.GetNetwork(plan.Bridge); err == nil {
		return nil
	} else if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get bridge %s: %w", plan.Bridge, err)
	}
	return def.CreateNetwork(api.NetworksPost{
		Name: plan.Bridge,
		Type: "bridge",
		NetworkPut: api.NetworkPut{
			Description: "Sandcastle v2 shared bridge for " + plan.Tenant,
			Config: api.ConfigMap{
				"ipv4.address": gatewayCIDR(plan.PrivateCIDR),
				"ipv4.nat":     "true",
				"ipv6.address": "none",
				// Flat per-tenant DNS: dnsmasq publishes <machine>.<suffix> for every
				// leased instance across all of the tenant's projects (ADR-0016).
				"dns.domain":        plan.DNSSuffix,
				meta.KeyKind:        "network",
				meta.KeyTenant:      plan.Tenant,
				meta.KeyPrivateCIDR: plan.PrivateCIDR,
				meta.KeyVersion:     "2",
			},
		},
	})
}

// ensureV2Project creates an infra or app project that references the shared
// bridge via features.networks=false. Infra shares the default image store
// (features.images=false) to avoid copying the base; app projects keep their
// own image store so tenants can pull their own images.
// v2 infra-project metadata keys. The infra project is the durable record of a
// tenant's shared settings so a later project-create (broker) can rebuild an app
// project + profile without the operator re-supplying them.
const (
	keyV2Bridge = "user.sandcastle.v2.bridge"
	keyV2Pool   = "user.sandcastle.v2.pool"
	keyV2Suffix = "user.sandcastle.v2.suffix"
	keyV2CIDR   = "user.sandcastle.v2.cidr"
	keyV2User   = "user.sandcastle.v2.user"
	keyV2SSHKey = "user.sandcastle.v2.sshkey"
	keyV2Prefix = "user.sandcastle.v2.prefix"
)

func v2InfraMetadata(plan tenant.CreatePlanV2) map[string]string {
	return map[string]string{
		keyV2Bridge: plan.Bridge,
		keyV2Pool:   plan.StoragePool,
		keyV2Suffix: plan.DNSSuffix,
		keyV2CIDR:   plan.PrivateCIDR,
		keyV2User:   plan.DefaultProfileUser,
		keyV2SSHKey: plan.SSHPublicKey,
		keyV2Prefix: plan.Prefix,
	}
}

func ensureV2Project(server TenantCreateServer, name string, description string, kind string, tenantName string, ownImages bool, extra map[string]string) error {
	if _, _, err := server.GetProject(name); err == nil {
		return nil
	} else if !api.StatusErrorCheck(err, http.StatusNotFound) && !api.StatusErrorCheck(err, http.StatusForbidden) {
		return fmt.Errorf("get project %s: %w", name, err)
	}
	config := api.ConfigMap{
		"features.networks":        "false",
		"features.images":          boolStr(ownImages),
		"features.profiles":        "true",
		"features.storage.volumes": "true",
		meta.KeyKind:               kind,
		meta.KeyTenant:             tenantName,
		meta.KeyVersion:            "2",
	}
	for k, v := range extra {
		config[k] = v
	}
	return server.CreateProject(api.ProjectsPost{
		Name: name,
		ProjectPut: api.ProjectPut{
			Description: description,
			Config:      config,
		},
	})
}

func ensureV2AppProfile(server TenantResourceServer, plan tenant.CreatePlanV2) error {
	desired := api.ProfilePut{
		Description: "Sandcastle v2 default profile for " + plan.Tenant,
		Config: api.ConfigMap{
			"cloud-init.user-data": tenant.V2DefaultProfileUserData(plan.DefaultProfileUser, plan.SSHPublicKey),
			meta.KeyKind:           "profile",
			meta.KeyTenant:         plan.Tenant,
			meta.KeyVersion:        "2",
		},
		Devices: api.DevicesMap{
			"root": {"type": "disk", "pool": plan.StoragePool, "path": "/"},
			"eth0": {"type": "nic", "nictype": "bridged", "parent": plan.Bridge},
		},
	}
	return ensureExactProfile(server, "default", desired)
}

func ensureV2SidecarProfile(server TenantResourceServer, plan tenant.CreatePlanV2) error {
	desired := api.ProfilePut{
		Description: "Sandcastle v2 sidecar profile for " + plan.Tenant,
		Config: api.ConfigMap{
			meta.KeyKind:    "sidecar",
			meta.KeyTenant:  plan.Tenant,
			meta.KeyVersion: "2",
		},
		Devices: api.DevicesMap{
			"root": {"type": "disk", "pool": plan.StoragePool, "path": "/"},
			"eth0": {"type": "nic", "nictype": "bridged", "parent": plan.Bridge, "ipv4.address": plan.DNSAddress},
		},
	}
	return ensureExactProfile(server, "sidecar", desired)
}

func ensureV2Sidecar(server TenantResourceServer, plan tenant.CreatePlanV2, image string) error {
	if _, _, err := server.GetInstance(plan.SidecarInstance); err == nil {
		return nil
	} else if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get sidecar %s: %w", plan.SidecarInstance, err)
	}
	// Accept a public-remote ref (images:debian/13), a fingerprint, or a local alias.
	source := imageInstanceSource(image)
	op, err := server.CreateInstance(api.InstancesPost{
		Name:   plan.SidecarInstance,
		Type:   "container",
		Start:  true,
		Source: source,
		InstancePut: api.InstancePut{
			Description: "Sandcastle v2 sidecar (CoreDNS + Tailscale + Caddy)",
			Config: api.ConfigMap{
				meta.KeyKind:    "sidecar",
				meta.KeyTenant:  plan.Tenant,
				meta.KeyVersion: "2",
			},
			Profiles: []string{"sidecar"},
		},
	})
	if err != nil {
		return fmt.Errorf("create sidecar %s: %w", plan.SidecarInstance, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for sidecar %s: %w", plan.SidecarInstance, err)
	}
	return nil
}

// coreDNSVersion is the CoreDNS release fetched onto stock-Debian sidecars
// (CoreDNS is not packaged in Debian apt, so we install the release binary).
const coreDNSVersion = "1.14.3"

// installV2SidecarPackages makes a stock Debian system container into a usable
// sidecar: it installs the CoreDNS release binary (not in apt) and Tailscale
// (via Tailscale's official apt repo). Idempotent — skips work already done, so
// re-running create is cheap. This replaces the prebuilt sandcastle/base image.
func installV2SidecarPackages(server TenantResourceServer, plan tenant.CreatePlanV2) error {
	script := strings.Join([]string{
		"set -eu",
		"export DEBIAN_FRONTEND=noninteractive",
		"need_apt=0",
		"command -v curl >/dev/null 2>&1 || need_apt=1",
		"command -v tailscale >/dev/null 2>&1 || need_apt=1",
		"if [ \"$need_apt\" = 1 ]; then apt-get update -qq && apt-get install -y -qq curl ca-certificates gnupg tar; fi",
		// CoreDNS release binary (arch-matched; dpkg arch names == coredns arch names).
		"if [ ! -x /usr/local/bin/coredns ]; then " +
			"ARCH=$(dpkg --print-architecture); " +
			"curl -fsSL \"https://github.com/coredns/coredns/releases/download/v" + coreDNSVersion +
			"/coredns_" + coreDNSVersion + "_linux_${ARCH}.tgz\" -o /tmp/coredns.tgz && " +
			"tar -xzf /tmp/coredns.tgz -C /usr/local/bin coredns && chmod 0755 /usr/local/bin/coredns && rm -f /tmp/coredns.tgz; fi",
		// Tailscale via its official apt repo (keyed to the container's codename).
		"if ! command -v tailscale >/dev/null 2>&1; then " +
			". /etc/os-release; " +
			"curl -fsSL \"https://pkgs.tailscale.com/stable/debian/${VERSION_CODENAME}.noarmor.gpg\" -o /usr/share/keyrings/tailscale-archive-keyring.gpg && " +
			"curl -fsSL \"https://pkgs.tailscale.com/stable/debian/${VERSION_CODENAME}.tailscale-keyring.list\" -o /etc/apt/sources.list.d/tailscale.list && " +
			"apt-get update -qq && apt-get install -y -qq tailscale; fi",
		"command -v /usr/local/bin/coredns >/dev/null && command -v tailscale >/dev/null",
	}, "\n")
	if err := execSidecar(server, plan.SidecarInstance, script); err != nil {
		return fmt.Errorf("install sidecar packages (CoreDNS+Tailscale): %w", err)
	}
	return nil
}

// configureV2Sidecar pins the sidecar's static IP (the base image does not DHCP
// eth0), writes the CoreDNS config, and starts CoreDNS. Tailscale is handled
// separately so the auth key can be omitted when re-running.
func configureV2Sidecar(server TenantResourceServer, plan tenant.CreatePlanV2) error {
	gateway, err := gatewayIPFromCIDR(plan.PrivateCIDR)
	if err != nil {
		return err
	}
	prefix, err := netip.ParsePrefix(plan.PrivateCIDR)
	if err != nil {
		return fmt.Errorf("parse CIDR %s: %w", plan.PrivateCIDR, err)
	}
	ipWithPrefix := fmt.Sprintf("%s/%d", plan.DNSAddress, prefix.Bits())

	// Static network: apply now + a systemd oneshot for reboot persistence.
	netScript := strings.Join([]string{
		"install -d -m 0755 /usr/local/sbin /etc/systemd/system/multi-user.target.wants",
		"printf '%s\\n' '#!/bin/sh' 'set -eu' '/usr/sbin/ip link set eth0 up' '/usr/sbin/ip addr replace " + ipWithPrefix + " dev eth0' '/usr/sbin/ip route replace default via " + gateway + "' > /usr/local/sbin/sandcastle-sidecar-network",
		"chmod 0755 /usr/local/sbin/sandcastle-sidecar-network",
		"printf '%s\\n' '[Unit]' 'Description=Sandcastle sidecar static network' 'Before=network-online.target' '' '[Service]' 'Type=oneshot' 'ExecStart=/usr/local/sbin/sandcastle-sidecar-network' 'RemainAfterExit=yes' '' '[Install]' 'WantedBy=multi-user.target' > /etc/systemd/system/sandcastle-sidecar-network.service",
		"ln -sf /etc/systemd/system/sandcastle-sidecar-network.service /etc/systemd/system/multi-user.target.wants/sandcastle-sidecar-network.service",
		"systemctl daemon-reload 2>/dev/null || true",
		"/usr/local/sbin/sandcastle-sidecar-network",
	}, " && ")
	if err := execSidecar(server, plan.SidecarInstance, netScript); err != nil {
		return fmt.Errorf("configure sidecar network: %w", err)
	}

	// Write CoreDNS files from the plan.
	for _, f := range plan.DNSFiles {
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := writeInstanceDir(server, plan.SidecarInstance, f.Path); err != nil {
			return err
		}
		if err := server.CreateInstanceFile(plan.SidecarInstance, f.Path, incus.InstanceFileArgs{
			Content:   strings.NewReader(f.Content),
			Type:      "file",
			Mode:      int(mode),
			WriteMode: "overwrite",
		}); err != nil {
			return fmt.Errorf("write %s to sidecar: %w", f.Path, err)
		}
	}

	// Start CoreDNS (free :53 from systemd-resolved first).
	coredns := strings.Join([]string{
		"systemctl stop systemd-resolved.service 2>/dev/null || true",
		"systemctl mask systemd-resolved.service 2>/dev/null || true",
		"printf '%s\\n' '[Unit]' 'Description=CoreDNS tenant resolver' 'After=network-online.target' '' '[Service]' 'ExecStart=/usr/local/bin/coredns -conf /etc/coredns/Corefile' 'Restart=on-failure' '' '[Install]' 'WantedBy=multi-user.target' > /etc/systemd/system/coredns.service",
		"systemctl daemon-reload",
		"systemctl enable --now coredns.service",
		"sleep 1",
		"systemctl is-active coredns.service",
	}, " && ")
	if err := execSidecar(server, plan.SidecarInstance, coredns); err != nil {
		return fmt.Errorf("start CoreDNS: %w", err)
	}
	return nil
}

// v2TailscaleUp brings the sidecar onto the tenant's tailnet. With an auth key it
// joins non-interactively and hard-gates on getting a tailnet IP. Without one it
// starts an interactive `tailscale up` as a detached unit (which blocks waiting for
// the user) and returns the login URL it prints, so the caller can show it; the
// sidecar joins once the user visits that URL. Returns "" when a key was used or the
// sidecar is already registered.
// v2TailscaleUp brings the sidecar onto the tenant's tailnet and, on the auth-key
// path, sets up the Incus Reach (ADR-0017): a raw-TCP `tailscale serve` forwarding
// the sidecar's tailnet :8443 to the host's Incus gateway, plus it returns the
// sidecar's tailnet IPv4 (the address the client's Incus remote points at).
// Returns (interactiveLoginURL, sidecarTailnetIP, error).
func v2TailscaleUp(server TenantResourceServer, plan tenant.CreatePlanV2, authKey string) (string, string, error) {
	base := "--advertise-routes=" + plan.PrivateCIDR + " --hostname=" + plan.SidecarInstance + " --accept-dns=false"
	if authKey == "" {
		const log = "/var/lib/sandcastle-tsup.log"
		script := strings.Join([]string{
			"set -e",
			"systemctl unmask tailscaled.service 2>/dev/null || true",
			"systemctl enable --now tailscaled.service",
			"for i in $(seq 1 30); do tailscale status >/dev/null 2>&1 && break; sleep 1; done",
			// Idempotent re-create: if already authenticated, nothing to show.
			"if tailscale ip -4 >/dev/null 2>&1; then exit 0; fi",
			// `tailscale up` blocks until the user authenticates, so run it as a
			// detached transient unit and read the login URL it prints to a file.
			"printf '#!/bin/sh\\nexec tailscale up " + base + " > " + log + " 2>&1\\n' > /usr/local/bin/sandcastle-tsup.sh",
			"chmod +x /usr/local/bin/sandcastle-tsup.sh",
			": > " + log,
			"systemctl reset-failed sandcastle-tsup.service 2>/dev/null || true",
			"systemd-run --unit=sandcastle-tsup --collect /usr/local/bin/sandcastle-tsup.sh >/dev/null 2>&1 || true",
			"for i in $(seq 1 30); do url=$(grep -Eom1 'https://login\\.tailscale\\.com/[A-Za-z0-9._/-]+' " + log + " || true); [ -n \"$url\" ] && { printf 'TSLOGINURL=%s\\n' \"$url\"; exit 0; }; sleep 1; done",
			"exit 0",
		}, "\n")
		out, err := execSidecarCapture(server, plan.SidecarInstance, script)
		if err != nil {
			return "", "", fmt.Errorf("tailscale up (interactive): %w", err)
		}
		return parseTailscaleLoginURL(out), "", nil
	}
	gateway, err := gatewayIPFromCIDR(plan.PrivateCIDR)
	if err != nil {
		return "", "", err
	}
	upCmd := "tailscale up --auth-key='" + authKey + "' " + base + " --timeout=60s"
	up := strings.Join([]string{
		"set -e",
		"systemctl unmask tailscaled.service 2>/dev/null || true",
		"systemctl enable --now tailscaled.service",
		// Wait for tailscaled to accept commands before bringing the link up —
		// on a freshly apt-installed sidecar the daemon socket lags the unit.
		"for i in $(seq 1 30); do tailscale status >/dev/null 2>&1 && break; sleep 1; done",
		// Bring up and confirm we actually got a tailnet IP (a bare `up` can
		// exit 0 without connecting); retry a couple of times if not.
		"for attempt in 1 2 3; do " + upCmd + " || true; tailscale ip -4 >/dev/null 2>&1 && break; sleep 3; done",
		// Hard gate: fail the create if the sidecar is not on the tailnet.
		"tailscale ip -4 >/dev/null 2>&1 || { echo 'tailscale did not connect' >&2; tailscale status >&2; exit 1; }",
		// Incus Reach (ADR-0017): proxy the host's Incus API onto the tenant
		// tailnet — raw TCP, TLS passes through so the host cert is pinned.
		"tailscale serve --bg --tcp=8443 tcp://" + gateway + ":8443",
		// Emit the sidecar's tailnet IPv4 for the caller (the client's remote addr).
		"printf 'TSIP=%s\\n' \"$(tailscale ip -4 | head -1)\"",
	}, "\n")
	out, err := execSidecarCapture(server, plan.SidecarInstance, up)
	if err != nil {
		return "", "", fmt.Errorf("tailscale up: %w", err)
	}
	return "", parseTailnetIP(out), nil
}

// parseTailnetIP extracts the `TSIP=<ipv4>` line emitted by the sidecar setup.
func parseTailnetIP(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "TSIP="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// parseTailscaleLoginURL extracts the URL emitted by the interactive up script.
func parseTailscaleLoginURL(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if url, ok := strings.CutPrefix(strings.TrimSpace(line), "TSLOGINURL="); ok {
			return strings.TrimSpace(url)
		}
	}
	return ""
}

func writeInstanceDir(server TenantResourceServer, instance string, filePath string) error {
	dir := filePath[:strings.LastIndex(filePath, "/")]
	if dir == "" {
		return nil
	}
	return execSidecar(server, instance, "mkdir -p "+dir)
}

// execSidecarCapture runs a script in the sidecar and returns its stdout.
func execSidecarCapture(server TenantResourceServer, instance string, script string) (string, error) {
	var stdout, stderr strings.Builder
	dataDone := make(chan bool)
	op, err := server.ExecInstance(instance, api.InstanceExecPost{
		Command:   []string{"/bin/sh", "-c", script},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		Stdout:   &stdout,
		Stderr:   &stderr,
		DataDone: dataDone,
	})
	if err != nil {
		return "", err
	}
	if err := op.Wait(); err != nil {
		return "", fmt.Errorf("%w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	<-dataDone
	return stdout.String(), nil
}

func execSidecar(server TenantResourceServer, instance string, script string) error {
	var stderr strings.Builder
	dataDone := make(chan bool)
	op, err := server.ExecInstance(instance, api.InstanceExecPost{
		Command:   []string{"/bin/sh", "-c", script},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		Stderr:   &stderr,
		DataDone: dataDone,
	})
	if err != nil {
		return err
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("%w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	<-dataDone
	return nil
}

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func looksLikeFingerprint(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 12 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
