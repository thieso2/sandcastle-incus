package tenant

import (
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
)

func TestPlanCreate(t *testing.T) {
	plan, err := PlanCreate(config.LoadAdminFromEnv(), CreateRequest{
		Reference: "acme",
		OccupiedCIDRs: []string{
			"10.248.0.0/24",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Reference != "acme" {
		t.Fatalf("Reference = %q", plan.Reference)
	}
	if plan.IncusProject != "sc-acme" {
		t.Fatalf("IncusProject = %q", plan.IncusProject)
	}
	if plan.DNSSuffix != "acme" {
		t.Fatalf("DNSSuffix = %q", plan.DNSSuffix)
	}
	if plan.PrivateCIDR != "10.248.1.0/24" {
		t.Fatalf("PrivateCIDR = %q", plan.PrivateCIDR)
	}
	if plan.TailscaleAddress != "10.248.1.2" {
		t.Fatalf("TailscaleAddress = %q", plan.TailscaleAddress)
	}
	if plan.DNSAddress != "10.248.1.53" {
		t.Fatalf("DNSAddress = %q", plan.DNSAddress)
	}
	if plan.TenantMetadataConfig[meta.KeyKind] != meta.KindTenant {
		t.Fatalf("metadata kind = %q", plan.TenantMetadataConfig[meta.KeyKind])
	}
	metadata, err := meta.ParseTenantConfig(plan.TenantMetadataConfig)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Tenant != "acme" {
		t.Fatalf("tenant metadata = %#v", metadata)
	}
	if len(metadata.Projects) != 1 || metadata.Projects[0].Name != naming.DefaultProjectName {
		t.Fatalf("projects = %#v", metadata.Projects)
	}
	if metadata.Tailscale.State != meta.TailscaleStateRunningLoggedOut {
		t.Fatalf("tailscale state = %q", metadata.Tailscale.State)
	}
	if len(plan.ImageAliases) != 2 || plan.ImageAliases[0] != config.DefaultBaseImageAlias || plan.ImageAliases[1] != config.DefaultAIImageAlias {
		t.Fatalf("ImageAliases = %#v", plan.ImageAliases)
	}
	if len(plan.Sidecars) != 2 {
		t.Fatalf("sidecars = %d, want 2", len(plan.Sidecars))
	}
	if plan.Sidecars[0].Name != TailscaleInstanceName(plan.IncusProject) || plan.Sidecars[0].Address != "10.248.1.2" {
		t.Fatalf("tailscale sidecar = %#v", plan.Sidecars[0])
	}
	if tun := plan.Sidecars[0].Devices["tun"]; tun["type"] != "unix-char" || tun["path"] != "/dev/net/tun" {
		t.Fatalf("tailscale tun device = %#v", tun)
	}
	if plan.Sidecars[1].Name != DNSName || plan.Sidecars[1].Address != "10.248.1.53" {
		t.Fatalf("dns sidecar = %#v", plan.Sidecars[1])
	}
	if _, ok := plan.Sidecars[1].Devices["tun"]; ok {
		t.Fatalf("dns sidecar should not have tun device: %#v", plan.Sidecars[1].Devices)
	}
	if len(plan.DNSFiles) != 2 {
		t.Fatalf("DNS files = %d, want 2", len(plan.DNSFiles))
	}
	if plan.TenantCA.CertificatePath != "/ca.crt" {
		t.Fatalf("CA certificate path = %q", plan.TenantCA.CertificatePath)
	}
	if len(plan.TenantCA.CertificatePEM) == 0 || len(plan.TenantCA.PrivateKeyPEM) == 0 {
		t.Fatal("expected generated tenant CA material")
	}
}

func TestPrivateNetworkNameUsesStableHashForLongTenantIncusProjectNames(t *testing.T) {
	first := PrivateNetworkName("sc-tenant-e2e-local-vm-20260521-1038")
	second := PrivateNetworkName("sc-tenant-e2e-local-vm-20260521-1039")
	if len(first) > 15 || len(second) > 15 {
		t.Fatalf("network names too long: %q %q", first, second)
	}
	if first == second {
		t.Fatalf("long tenant network names collided: %q", first)
	}
	if PrivateNetworkName("sc-acme") != "sc-acme" {
		t.Fatalf("short tenant network name changed")
	}
}

func TestPlanCreateRejectsInvalidReference(t *testing.T) {
	_, err := PlanCreate(config.LoadAdminFromEnv(), CreateRequest{Reference: "Acme"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanCreateRejectsDeniedTenantSuffix(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.DeniedDomainSuffixes = []string{"corp"}
	_, err := PlanCreate(admin, CreateRequest{Reference: "corp"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanCreateRejectsKnownPublicTLD(t *testing.T) {
	_, err := PlanCreate(config.LoadAdminFromEnv(), CreateRequest{Reference: "com"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanCreateAllowsExplicitLabSuffixOverride(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.AllowedDomainSuffixes = []string{"test"}
	plan, err := PlanCreate(admin, CreateRequest{Reference: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.DNSSuffix != "test" {
		t.Fatalf("DNSSuffix = %q", plan.DNSSuffix)
	}
}
