package incusx

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	cliconfig "github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/lxc/incus/v6/shared/api"

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
	if err := ensureV2Project(server, plan.InfraProject, "Sandcastle v2 infra for "+plan.Tenant, "infra", plan.Tenant, false); err != nil {
		return err
	}
	c.log("ensure app project " + plan.DefaultProject)
	if err := ensureV2Project(server, plan.DefaultProject, "Sandcastle v2 project default for "+plan.Tenant, "project", plan.Tenant, true); err != nil {
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
	c.log("configure sidecar network + CoreDNS")
	if err := configureV2Sidecar(server.UseProject(plan.InfraProject), plan); err != nil {
		return err
	}
	if strings.TrimSpace(opts.TailscaleAuthKey) != "" {
		c.log("tailscale up (advertise " + plan.PrivateCIDR + ")")
		if err := v2TailscaleUp(server.UseProject(plan.InfraProject), plan, opts.TailscaleAuthKey); err != nil {
			return err
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
func ensureV2Project(server TenantCreateServer, name string, description string, kind string, tenantName string, ownImages bool) error {
	if _, _, err := server.GetProject(name); err == nil {
		return nil
	} else if !api.StatusErrorCheck(err, http.StatusNotFound) && !api.StatusErrorCheck(err, http.StatusForbidden) {
		return fmt.Errorf("get project %s: %w", name, err)
	}
	return server.CreateProject(api.ProjectsPost{
		Name: name,
		ProjectPut: api.ProjectPut{
			Description: description,
			Config: api.ConfigMap{
				"features.networks":        "false",
				"features.images":          boolStr(ownImages),
				"features.profiles":        "true",
				"features.storage.volumes": "true",
				meta.KeyKind:               kind,
				meta.KeyTenant:             tenantName,
				meta.KeyVersion:            "2",
			},
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
	source := api.InstanceSource{Type: "image"}
	// Accept either an alias or a fingerprint for the system-container base.
	if looksLikeFingerprint(image) {
		source.Fingerprint = image
	} else {
		source.Alias = image
	}
	op, err := server.CreateInstance(api.InstancesPost{
		Name:  plan.SidecarInstance,
		Type:  "container",
		Start: true,
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

func v2TailscaleUp(server TenantResourceServer, plan tenant.CreatePlanV2, authKey string) error {
	up := strings.Join([]string{
		"systemctl unmask tailscaled.service 2>/dev/null || true",
		"systemctl enable --now tailscaled.service",
		"sleep 2",
		"tailscale up --auth-key='" + authKey + "' --advertise-routes=" + plan.PrivateCIDR +
			" --hostname=" + plan.SidecarInstance + " --accept-dns=false --timeout=60s",
		"tailscale status >/dev/null 2>&1",
	}, " && ")
	if err := execSidecar(server, plan.SidecarInstance, up); err != nil {
		return fmt.Errorf("tailscale up: %w", err)
	}
	return nil
}

func writeInstanceDir(server TenantResourceServer, instance string, filePath string) error {
	dir := filePath[:strings.LastIndex(filePath, "/")]
	if dir == "" {
		return nil
	}
	return execSidecar(server, instance, "mkdir -p "+dir)
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
