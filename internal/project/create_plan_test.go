package project

import (
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
)

func TestPlanCreate(t *testing.T) {
	plan, err := PlanCreate(config.LoadAdminFromEnv(), CreateRequest{
		Reference: "alice/myproject",
		Domain:    "MyProject.Project-TLD.",
		OccupiedCIDRs: []string{
			"10.248.0.0/24",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Reference != "alice/myproject" {
		t.Fatalf("Reference = %q", plan.Reference)
	}
	if plan.IncusProject != "sc-alice-myproject" {
		t.Fatalf("IncusProject = %q", plan.IncusProject)
	}
	if plan.Domain != "myproject.project-tld" {
		t.Fatalf("Domain = %q", plan.Domain)
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
	if plan.ProjectMetadataConfig[meta.KeyKind] != meta.KindProject {
		t.Fatalf("metadata kind = %q", plan.ProjectMetadataConfig[meta.KeyKind])
	}
	if len(plan.Sidecars) != 2 {
		t.Fatalf("sidecars = %d, want 2", len(plan.Sidecars))
	}
	if plan.Sidecars[0].Name != TailscaleName || plan.Sidecars[0].Address != "10.248.1.2" {
		t.Fatalf("tailscale sidecar = %#v", plan.Sidecars[0])
	}
	if plan.Sidecars[1].Name != DNSName || plan.Sidecars[1].Address != "10.248.1.53" {
		t.Fatalf("dns sidecar = %#v", plan.Sidecars[1])
	}
	if len(plan.DNSFiles) != 2 {
		t.Fatalf("DNS files = %d, want 2", len(plan.DNSFiles))
	}
	if plan.ProjectCA.CertificatePath != "/ca.crt" {
		t.Fatalf("CA certificate path = %q", plan.ProjectCA.CertificatePath)
	}
	if len(plan.ProjectCA.CertificatePEM) == 0 || len(plan.ProjectCA.PrivateKeyPEM) == 0 {
		t.Fatal("expected generated project CA material")
	}
}

func TestPlanCreateRejectsInvalidReference(t *testing.T) {
	_, err := PlanCreate(config.LoadAdminFromEnv(), CreateRequest{
		Reference: "Alice/myproject",
		Domain:    "myproject.project-tld",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanCreateRejectsInvalidDomain(t *testing.T) {
	_, err := PlanCreate(config.LoadAdminFromEnv(), CreateRequest{
		Reference: "alice/myproject",
		Domain:    "bad domain",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
