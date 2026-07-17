package tenant

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/certs"
	"github.com/thieso2/sandcastle-incus/internal/cidr"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/dns"
	domainrules "github.com/thieso2/sandcastle-incus/internal/domain"
	"github.com/thieso2/sandcastle-incus/internal/naming"
)

// V2DefaultProfileUserData renders the cloud-init user-data baked into a v2
// project's default profile. Freeform `incus launch` of a cloud-init image
// applies it at first boot, creating the login user (UID 2000, sudo) with the
// tenant's SSH key and an enabled sshd — so machines are reachable over the
// tenant's tailnet with no Sandcastle-in-the-loop configure step.
//
// When project and suffix are known the user-data is a jinja template that
// stamps each machine with its canonical Machine Private Hostname
// <machine>.<project>.<suffix> (ADR-0018) — identity only; resolution comes
// from the sidecar CoreDNS zone.
func V2DefaultProfileUserData(user string, sshKey string, project string, suffix string, signerURL string) string {
	header := "#cloud-config\n"
	identity := ""
	project = strings.TrimSpace(project)
	suffix = strings.TrimSpace(suffix)
	signerURL = strings.TrimRight(strings.TrimSpace(signerURL), "/")
	jinja := project != "" && suffix != ""
	if jinja {
		header = "## template: jinja\n#cloud-config\n"
		identity = fmt.Sprintf("fqdn: {{ v1.local_hostname }}.%s.%s\nprefer_fqdn_over_hostname: true\n", project, suffix)
	}
	body := fmt.Sprintf(`users:
  - name: %s
    uid: 2000
    groups: [sudo]
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    ssh_authorized_keys:
      - %s
packages:
  - openssh-server
`, user, sshKey)

	// Caddy HTTPS ingress (ADR-0011): only when we know the machine's identity
	// (jinja) and where to fetch its leaf (the sidecar signer). The setup script
	// is base64-embedded to sidestep YAML/indentation pitfalls; machine.env
	// carries the per-machine FQDN (jinja) and signer URL.
	if jinja && signerURL != "" {
		script := base64.StdEncoding.EncodeToString([]byte(caddyIngressSetupScript))
		generalize := base64.StdEncoding.EncodeToString([]byte(machineGeneralizeScript))
		body += fmt.Sprintf(`write_files:
  - path: /etc/sandcastle/machine.env
    permissions: '0644'
    content: |
      FQDN={{ v1.local_hostname }}.%s.%s
      SIGNER=%s
      HOME=/home/%s
  - path: /usr/local/sbin/sandcastle-generalize
    permissions: '0755'
    encoding: b64
    content: %s
  - path: /usr/local/sbin/sandcastle-caddy-setup
    permissions: '0755'
    encoding: b64
    content: %s
runcmd:
  - [/usr/local/sbin/sandcastle-generalize]
  - [systemctl, enable, --now, ssh]
  - [/usr/local/sbin/sandcastle-caddy-setup]
`, project, suffix, signerURL, user, generalize, script)
		return header + identity + body
	}

	return header + identity + body + `runcmd:
  - [systemctl, enable, --now, ssh]
`
}

// machineGeneralizeScript freshens per-instance identity so a machine launched
// from an `sc image save` base image does NOT inherit the source machine's SSH
// host keys, machine-id, or stale TLS leaf. It runs once per instance (cloud-init
// per-instance runcmd) before sshd is (re)started. On a fresh stock machine the
// identity is already unique, so every step is a harmless no-op — correctness
// lives here in one place rather than at save time.
const machineGeneralizeScript = `#!/bin/bash
set -u
# Drop the source machine's host identity + stale leaf (re-fetched by caddy-setup).
rm -f /etc/ssh/ssh_host_* /etc/sandcastle/tls/cert.pem /etc/sandcastle/tls/key.pem
ssh-keygen -A >/dev/null 2>&1 || true
# Remove (not truncate) machine-id so systemd-machine-id-setup mints a fresh one;
# a leftover empty read-only file is not reliably regenerated in a container.
rm -f /etc/machine-id /var/lib/dbus/machine-id
systemd-machine-id-setup >/dev/null 2>&1 || true
# A cloned image had sshd enabled, so it is already serving the now-deleted keys;
# restart it (if running) to pick up the freshly generated host keys.
systemctl try-restart ssh >/dev/null 2>&1 || true
`

// caddyIngressSetupScript installs Caddy, trusts the tenant CA, fetches this
// machine's leaf from the sidecar signer, writes the Caddyfile, and (re)starts
// Caddy as root. It sources /etc/sandcastle/machine.env for FQDN + SIGNER.
const caddyIngressSetupScript = `#!/bin/bash
set -eu
. /etc/sandcastle/machine.env
export DEBIAN_FRONTEND=noninteractive
install -d -m 0755 /etc/sandcastle/tls /usr/local/share/ca-certificates /etc/caddy /etc/systemd/system/caddy.service.d

# Install Caddy from its official repo (not in stock Debian apt).
if ! command -v caddy >/dev/null 2>&1; then
  apt-get install -y -qq debian-keyring debian-archive-keyring apt-transport-https curl gnupg
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' > /etc/apt/sources.list.d/caddy-stable.list
  apt-get update -qq
  apt-get install -y -qq caddy
fi

# Trust the tenant CA on this machine (machine-to-machine HTTPS).
curl -fsS "$SIGNER/tls/ca" -o /usr/local/share/ca-certificates/sandcastle-tenant.crt && update-ca-certificates || true

# Fetch this machine's leaf (key+cert) BEFORE Caddy serves.
curl -fsS "$SIGNER/tls/leaf?fqdn=$FQDN" | python3 -c 'import json,sys;d=json.load(sys.stdin);open("/etc/sandcastle/tls/cert.pem","w").write(d["cert"]);open("/etc/sandcastle/tls/key.pem","w").write(d["key"])'
chmod 600 /etc/sandcastle/tls/key.pem

# Caddyfile: HTTPS with our leaf (auto HTTP->HTTPS redirect), /_h browses the
# login user's $HOME, /_w browses /workspace, everything else proxies to :3000
# with Host preserved. redir handles the bare /_h and /_w (no trailing slash).
cat > /etc/caddy/Caddyfile <<EOF
$FQDN, *.$FQDN {
    tls /etc/sandcastle/tls/cert.pem /etc/sandcastle/tls/key.pem
    redir /_h /_h/
    redir /_w /_w/
    handle_path /_h/* {
        root * $HOME
        file_server browse
    }
    handle_path /_w/* {
        root * /workspace
        file_server browse
    }
    handle {
        reverse_proxy localhost:3000
    }
}
EOF

# Caddy runs as root so it can read $HOME/... regardless of owner and bind :443.
printf '%s\n' '[Service]' 'User=root' 'Group=root' 'AmbientCapabilities=' > /etc/systemd/system/caddy.service.d/override.conf
systemctl daemon-reload
systemctl enable caddy
systemctl restart caddy
`

// DefaultV2UnixUser is the login user baked into a v2 project's default
// profile when the create request does not specify one. It matches the UID-2000
// convention (ADR-0014) applied when the profile is materialized.
const DefaultV2UnixUser = "dev"

// CreatePlanV2 describes the v2 MVP tenant bring-up (ADR-0016): one per-tenant
// infra project holding a single sidecar (CoreDNS + Tailscale + Caddy), one
// shared per-tenant bridge, and a seeded default app project. Machines are
// created later by the tenant with native incus into app projects on the shared
// bridge; DNS is flat (<machine>.<suffix>).
type CreatePlanV2 struct {
	Tenant         string `json:"tenant"`
	Prefix         string `json:"prefix"`
	InfraProject   string `json:"infraProject"`
	DefaultProject string `json:"defaultProject"`
	// DefaultProjectShort is the short name of the tenant's one project (issue
	// #93). The user chooses it at first login; it defaults to "default". The
	// full Incus project name is DefaultProject (<prefix>-<tenant>-<short>).
	DefaultProjectShort string     `json:"defaultProjectShort"`
	Bridge              string     `json:"bridge"`
	DNSSuffix           string     `json:"dnsSuffix"`
	PrivateCIDR         string     `json:"privateCIDR"`
	GatewayAddress      string     `json:"gatewayAddress"`
	TailscaleAddress    string     `json:"tailscaleAddress"`
	DNSAddress          string     `json:"dnsAddress"`
	StoragePool         string     `json:"storagePool"`
	HomeVolume          string     `json:"homeVolume"`
	WorkspaceVolume     string     `json:"workspaceVolume"`
	CAVolume            string     `json:"caVolume"`
	SidecarInstance     string     `json:"sidecarInstance"`
	SidecarImage        string     `json:"sidecarImage"`
	DefaultProfileUser  string     `json:"defaultProfileUser"`
	SSHPublicKey        string     `json:"sshPublicKey"`
	ImageAliases        []string   `json:"imageAliases"`
	DNSFiles            []dns.File `json:"dnsFiles"`
	TenantCA            TenantCA   `json:"tenantCA"`
	RestrictedProjects  []string   `json:"restrictedProjects"`
}

// PlanCreateV2 builds a CreatePlanV2 from admin config and a create request.
// It is pure (aside from CA key generation and the current time) so it can be
// unit tested without touching Incus.
func PlanCreateV2(admin config.Admin, request CreateRequest) (CreatePlanV2, error) {
	if err := admin.Validate(); err != nil {
		return CreatePlanV2{}, err
	}
	ref, err := naming.ParseTenantRef(request.Reference)
	if err != nil {
		return CreatePlanV2{}, err
	}
	prefix := naming.NormalizeV2Prefix(admin.IncusProjectPrefix)
	infraProject, err := naming.V2TenantInfraProjectName(prefix, ref.Tenant)
	if err != nil {
		return CreatePlanV2{}, err
	}
	// The tenant's one project (issue #93): the user names it at first login;
	// on re-login the stored short name is reused; blank on both ⇒ "default".
	// Not immutable — unlike the DNS suffix — so a differing request just wins.
	defaultProjectShort := strings.TrimSpace(request.InitialProject)
	if defaultProjectShort == "" {
		defaultProjectShort = strings.TrimSpace(request.ExistingDefaultProject)
	}
	if defaultProjectShort == "" {
		defaultProjectShort = naming.DefaultProjectName
	}
	if err := naming.ValidateProjectName(defaultProjectShort); err != nil {
		// Bad user input — no retry can fix a rejected project name.
		return CreatePlanV2{}, TerminalProvisionError{Err: err}
	}
	defaultProject, err := naming.V2ProjectName(prefix, ref.Tenant, defaultProjectShort)
	if err != nil {
		return CreatePlanV2{}, err
	}
	bridge, err := naming.V2BridgeName(prefix, ref.Tenant)
	if err != nil {
		return CreatePlanV2{}, err
	}
	unixUser := strings.TrimSpace(request.UnixUser)
	if unixUser == "" {
		unixUser = DefaultV2UnixUser
	}
	if err := naming.ValidateUnixUsername(unixUser); err != nil {
		return CreatePlanV2{}, err
	}
	requestedSuffix := strings.TrimSpace(request.DNSSuffix)
	existingSuffix := strings.TrimSpace(request.ExistingDNSSuffix)
	if requestedSuffix != "" && existingSuffix != "" && requestedSuffix != existingSuffix {
		return CreatePlanV2{}, TerminalProvisionError{Err: fmt.Errorf("the Tenant DNS Suffix is immutable: tenant %s already uses %q (requested %q)", ref.Tenant, existingSuffix, requestedSuffix)}
	}
	effectiveSuffix := requestedSuffix
	if effectiveSuffix == "" {
		effectiveSuffix = existingSuffix
	}
	if effectiveSuffix == "" {
		effectiveSuffix = ref.Tenant
	}
	suffix, err := domainrules.ValidateTenantDNSSuffix(effectiveSuffix, domainrules.Policy{
		AllowedSuffixes: admin.AllowedDomainSuffixes,
		DeniedSuffixes:  admin.DeniedDomainSuffixes,
	})
	if err != nil {
		// Bad user input — no retry can fix a rejected suffix.
		return CreatePlanV2{}, TerminalProvisionError{Err: err}
	}
	var tenantCIDR netip.Prefix
	if pref := strings.TrimSpace(request.PreferredCIDR); pref != "" {
		// Reuse the tenant's existing /24 (idempotent re-provision).
		tenantCIDR, err = netip.ParsePrefix(pref)
		if err != nil {
			return CreatePlanV2{}, fmt.Errorf("parse preferred CIDR %q: %w", pref, err)
		}
		tenantCIDR = tenantCIDR.Masked()
		// A reused CIDR must still come from this install's pool: anything
		// else means the reuse scan picked up a foreign install's tenant (or
		// the pool changed), and provisioning it would stand up a bridge on
		// address space this install doesn't own.
		if pool, perr := netip.ParsePrefix(strings.TrimSpace(admin.CIDRPool)); perr == nil {
			if !pool.Contains(tenantCIDR.Addr()) || tenantCIDR.Bits() < pool.Bits() {
				return CreatePlanV2{}, fmt.Errorf("preferred CIDR %s is outside the tenant CIDR pool %s", tenantCIDR, pool)
			}
		}
	} else if tenantCIDR, err = cidr.Allocate(admin.CIDRPool, cidr.DefaultTenantPrefixBits, request.OccupiedCIDRs); err != nil {
		return CreatePlanV2{}, err
	}
	gatewayAddress, err := roleAddress(tenantCIDR, cidr.GatewayHostOctet)
	if err != nil {
		return CreatePlanV2{}, err
	}
	tailscaleAddress, err := roleAddress(tenantCIDR, cidr.TailscaleHostOctet)
	if err != nil {
		return CreatePlanV2{}, err
	}
	dnsAddress, err := roleAddress(tenantCIDR, cidr.DNSHostOctet)
	if err != nil {
		return CreatePlanV2{}, err
	}
	dnsFiles, err := dns.RenderInitial(suffix, dnsAddress.String())
	if err != nil {
		return CreatePlanV2{}, err
	}
	ca, err := certs.GenerateCA("Sandcastle "+ref.String()+" tenant CA", time.Now().UTC())
	if err != nil {
		return CreatePlanV2{}, err
	}

	return CreatePlanV2{
		Tenant:              ref.Tenant,
		Prefix:              prefix,
		InfraProject:        infraProject,
		DefaultProject:      defaultProject,
		DefaultProjectShort: defaultProjectShort,
		Bridge:              bridge,
		DNSSuffix:           suffix,
		PrivateCIDR:         tenantCIDR.String(),
		GatewayAddress:      gatewayAddress.String(),
		TailscaleAddress:    tailscaleAddress.String(),
		DNSAddress:          dnsAddress.String(),
		StoragePool:         admin.StoragePool,
		HomeVolume:          HomeVolumeName,
		WorkspaceVolume:     WorkspaceVolumeName,
		CAVolume:            CAVolumeName,
		SidecarInstance:     naming.V2SidecarInstanceName,
		SidecarImage:        admin.Images.Base,
		DefaultProfileUser:  unixUser,
		SSHPublicKey:        request.SSHPublicKey,
		ImageAliases:        uniqueImageAliases(admin.Images.Base, admin.Images.AI),
		DNSFiles:            dnsFiles,
		TenantCA: TenantCA{
			CertificatePath: TenantCACertPath,
			PrivateKeyPath:  TenantCAKeyPath,
			CertificatePEM:  ca.CertificatePEM,
			PrivateKeyPEM:   ca.PrivateKeyPEM,
		},
		RestrictedProjects: []string{defaultProject},
	}, nil
}

// DNSAddressForCIDR returns the sidecar's address inside a tenant's private /24
// — the host that serves CoreDNS and the TLS leaf signer.
//
// Callers that re-render an app project's default profile must supply it: the
// profile's cloud-init embeds `http://<dns address>:<signer port>` as the machine
// Caddy's signer URL, and an empty address yields `http://:9443`, so the machine
// can never fetch its leaf and serves no HTTPS at all.
func DNSAddressForCIDR(privateCIDR string) (string, error) {
	privateCIDR = strings.TrimSpace(privateCIDR)
	if privateCIDR == "" {
		return "", fmt.Errorf("tenant private CIDR is empty")
	}
	prefix, err := netip.ParsePrefix(privateCIDR)
	if err != nil {
		return "", fmt.Errorf("parse tenant private CIDR %q: %w", privateCIDR, err)
	}
	address, err := roleAddress(prefix, cidr.DNSHostOctet)
	if err != nil {
		return "", err
	}
	return address.String(), nil
}
