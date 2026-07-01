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
	if plan.SidecarInstance != "sc2-acme" {
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
	// Flat zone named after the suffix, with fallthrough to the bridge dnsmasq.
	if !strings.Contains(corefile, "acme:53") {
		t.Fatalf("Corefile missing acme zone: %q", corefile)
	}
	if !strings.Contains(corefile, "fallthrough") {
		t.Fatalf("Corefile missing fallthrough: %q", corefile)
	}
	if !strings.Contains(corefile, "forward . "+plan.GatewayAddress) {
		t.Fatalf("Corefile missing gateway forward: %q", corefile)
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
