package tenant

import (
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
)

func v2TestAdmin() config.Admin {
	return config.Admin{
		Remote:                "big",
		StoragePool:           "default",
		CIDRPool:              "10.249.0.0/16",
		IncusProjectPrefix:    "sc2",
		InfrastructureProject: "sc-infra",
		Images:                config.Images{Base: "base", AI: "ai"},
	}
}

func TestPlanCreateV2Names(t *testing.T) {
	plan, err := PlanCreateV2(v2TestAdmin(), CreateRequest{Reference: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.InfraProject != "sc2-acme" {
		t.Fatalf("InfraProject = %q", plan.InfraProject)
	}
	if plan.DefaultProject != "sc2-acme-default" {
		t.Fatalf("DefaultProject = %q", plan.DefaultProject)
	}
	if plan.Bridge != "sc2-acme" {
		t.Fatalf("Bridge = %q", plan.Bridge)
	}
	if plan.SidecarInstance != "sidecar" {
		t.Fatalf("SidecarInstance = %q", plan.SidecarInstance)
	}
	if plan.DNSSuffix != "acme" {
		t.Fatalf("DNSSuffix = %q", plan.DNSSuffix)
	}
	if len(plan.RestrictedProjects) != 1 || plan.RestrictedProjects[0] != "sc2-acme-default" {
		t.Fatalf("RestrictedProjects = %v", plan.RestrictedProjects)
	}
	if plan.StoragePool != "default" {
		t.Fatalf("StoragePool = %q, want default", plan.StoragePool)
	}
}

func TestPlanCreateV2PreferredCIDRReused(t *testing.T) {
	// Re-provisioning an existing tenant reuses its /24 rather than allocating
	// a fresh one from the pool.
	plan, err := PlanCreateV2(v2TestAdmin(), CreateRequest{Reference: "acme", PreferredCIDR: "10.249.7.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.PrivateCIDR != "10.249.7.0/24" {
		t.Fatalf("PrivateCIDR = %q, want 10.249.7.0/24 (reused)", plan.PrivateCIDR)
	}
	if plan.GatewayAddress != "10.249.7.1" || plan.DNSAddress != "10.249.7.3" {
		t.Fatalf("role addresses off the reused CIDR: gw=%q dns=%q", plan.GatewayAddress, plan.DNSAddress)
	}
}

func TestPlanCreateV2PreferredCIDROutsidePoolRejected(t *testing.T) {
	// A preferred CIDR outside the install's pool means the reuse scan picked
	// up a foreign install's tenant (e.g. a v1 bridge on the same host) — that
	// must fail at planning, not as a dnsmasq bind error at bridge creation.
	_, err := PlanCreateV2(v2TestAdmin(), CreateRequest{Reference: "acme", PreferredCIDR: "10.248.1.0/24"})
	if err == nil {
		t.Fatal("want error for preferred CIDR outside pool 10.249.0.0/16")
	}
	if !strings.Contains(err.Error(), "outside the tenant CIDR pool") {
		t.Fatalf("err = %v, want pool-containment error", err)
	}
}

func TestPlanCreateV2RoleAddresses(t *testing.T) {
	plan, err := PlanCreateV2(v2TestAdmin(), CreateRequest{Reference: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(plan.GatewayAddress, ".1") {
		t.Fatalf("GatewayAddress = %q, want .1", plan.GatewayAddress)
	}
	if !strings.HasSuffix(plan.TailscaleAddress, ".2") {
		t.Fatalf("TailscaleAddress = %q, want .2", plan.TailscaleAddress)
	}
	if !strings.HasSuffix(plan.DNSAddress, ".3") {
		t.Fatalf("DNSAddress = %q, want .3", plan.DNSAddress)
	}
	if !strings.HasPrefix(plan.PrivateCIDR, "10.249.") {
		t.Fatalf("PrivateCIDR = %q, want 10.249.x", plan.PrivateCIDR)
	}
}

func TestPlanCreateV2FlatDNSZone(t *testing.T) {
	plan, err := PlanCreateV2(v2TestAdmin(), CreateRequest{Reference: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	var corefile string
	for _, f := range plan.DNSFiles {
		if strings.HasSuffix(f.Path, "Corefile") {
			corefile = f.Content
		}
	}
	if corefile == "" {
		t.Fatal("no Corefile in plan")
	}
	// Zone named after the suffix; the zone is the ONLY authority (ADR-0018) —
	// no fallthrough and no gateway-dnsmasq forwarding.
	if !strings.Contains(corefile, "acme:53") {
		t.Fatalf("Corefile missing acme zone: %q", corefile)
	}
	if strings.Contains(corefile, "fallthrough") {
		t.Fatalf("Corefile must not fall through to dnsmasq: %q", corefile)
	}
	if strings.Contains(corefile, "forward . "+plan.GatewayAddress) {
		t.Fatalf("Corefile must not forward the zone to the gateway: %q", corefile)
	}
}

func TestPlanCreateV2GeneratesCA(t *testing.T) {
	plan, err := PlanCreateV2(v2TestAdmin(), CreateRequest{Reference: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.TenantCA.CertificatePEM) == 0 || len(plan.TenantCA.PrivateKeyPEM) == 0 {
		t.Fatal("expected tenant CA material")
	}
}

func TestPlanCreateV2RejectsBadTenant(t *testing.T) {
	if _, err := PlanCreateV2(v2TestAdmin(), CreateRequest{Reference: "Bad Name"}); err == nil {
		t.Fatal("expected error for invalid tenant")
	}
}

// The boot cloud-init generalizes per-instance identity BEFORE sshd starts, so a
// machine launched from an `sc image save` base gets fresh SSH host keys /
// machine-id rather than the source machine's. Order matters: generalize, then
// ssh enable, then caddy setup.
func TestV2DefaultProfileUserDataGeneralizesBeforeSSH(t *testing.T) {
	data := V2DefaultProfileUserData("dev", "ssh-ed25519 AAAA", "default", "acme", "http://10.0.0.3:9443")

	for _, want := range []string{
		"/usr/local/sbin/sandcastle-generalize",
		"- [/usr/local/sbin/sandcastle-generalize]",
		"- [systemctl, enable, --now, ssh]",
		"- [/usr/local/sbin/sandcastle-caddy-setup]",
	} {
		if !strings.Contains(data, want) {
			t.Fatalf("user-data missing %q:\n%s", want, data)
		}
	}

	genRun := strings.Index(data, "- [/usr/local/sbin/sandcastle-generalize]")
	sshRun := strings.Index(data, "- [systemctl, enable, --now, ssh]")
	caddyRun := strings.Index(data, "- [/usr/local/sbin/sandcastle-caddy-setup]")
	if !(genRun < sshRun && sshRun < caddyRun) {
		t.Fatalf("runcmd order wrong: generalize=%d ssh=%d caddy=%d", genRun, sshRun, caddyRun)
	}

	// The generalize script must regenerate host identity and drop the stale leaf.
	for _, want := range []string{"ssh-keygen -A", "/etc/machine-id", "/etc/ssh/ssh_host_", "/etc/sandcastle/tls/cert.pem"} {
		if !strings.Contains(machineGeneralizeScript, want) {
			t.Fatalf("generalize script missing %q", want)
		}
	}
}

// Without a signer URL (identity unknown) there is no Caddy/generalize wiring —
// just ssh — so the fallback path stays minimal.
func TestV2DefaultProfileUserDataNoSignerIsMinimal(t *testing.T) {
	data := V2DefaultProfileUserData("dev", "ssh-ed25519 AAAA", "", "", "")
	if strings.Contains(data, "sandcastle-generalize") || strings.Contains(data, "sandcastle-caddy-setup") {
		t.Fatalf("fallback user-data should not wire generalize/caddy:\n%s", data)
	}
	if !strings.Contains(data, "- [systemctl, enable, --now, ssh]") {
		t.Fatalf("fallback user-data should still enable ssh:\n%s", data)
	}
}

// ADR-0018: the Tenant DNS Suffix is tenant-chosen (default: tenant name) and
// immutable across re-provisioning.
func TestPlanCreateV2DNSSuffix(t *testing.T) {
	plan, err := PlanCreateV2(v2TestAdmin(), CreateRequest{Reference: "acme", DNSSuffix: "corp"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.DNSSuffix != "corp" {
		t.Fatalf("DNSSuffix = %q, want corp", plan.DNSSuffix)
	}

	// default: tenant name
	plan, err = PlanCreateV2(v2TestAdmin(), CreateRequest{Reference: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.DNSSuffix != "acme" {
		t.Fatalf("DNSSuffix = %q, want acme", plan.DNSSuffix)
	}

	// re-provision reuses the stored suffix
	plan, err = PlanCreateV2(v2TestAdmin(), CreateRequest{Reference: "acme", ExistingDNSSuffix: "corp"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.DNSSuffix != "corp" {
		t.Fatalf("DNSSuffix = %q, want the existing corp", plan.DNSSuffix)
	}

	// immutable: differing explicit suffix is rejected
	if _, err = PlanCreateV2(v2TestAdmin(), CreateRequest{Reference: "acme", DNSSuffix: "other", ExistingDNSSuffix: "corp"}); err == nil {
		t.Fatal("expected immutability error")
	}

	// multi-label still rejected (single label for now)
	if _, err = PlanCreateV2(v2TestAdmin(), CreateRequest{Reference: "acme", DNSSuffix: "corp.internal"}); err == nil {
		t.Fatal("expected single-label validation error")
	}

	// public TLDs stay denied
	if _, err = PlanCreateV2(v2TestAdmin(), CreateRequest{Reference: "acme", DNSSuffix: "dev"}); err == nil {
		t.Fatal("expected public-TLD denial")
	}
}
