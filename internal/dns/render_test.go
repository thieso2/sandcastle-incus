package dns

import (
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
)

func TestRenderInitial(t *testing.T) {
	files, err := RenderInitial("Acme.", "10.248.0.53")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("len(files) = %d, want 3", len(files))
	}
	if files[0].Path != "/etc/coredns/Corefile" {
		t.Fatalf("Corefile path = %q", files[0].Path)
	}
	if !strings.Contains(files[0].Content, "acme:53") {
		t.Fatalf("Corefile content = %q", files[0].Content)
	}
	if !strings.Contains(files[0].Content, "forward . /etc/resolv.conf") {
		t.Fatalf("Corefile missing upstream forwarder: %q", files[0].Content)
	}
	if !strings.Contains(files[0].Content, "force_tcp") {
		t.Fatalf("Corefile missing TCP upstream forwarding: %q", files[0].Content)
	}
	if files[1].Path != "/etc/coredns/zones/db.acme" {
		t.Fatalf("zone path = %q", files[1].Path)
	}
	if !strings.Contains(files[1].Content, "ns IN A 10.248.0.53") {
		t.Fatalf("zone content = %q", files[1].Content)
	}
	if strings.Contains(files[1].Content, "*") {
		t.Fatalf("initial zone should not contain wildcards: %q", files[1].Content)
	}
	if files[2].Path != "/etc/resolv.conf" || !strings.Contains(files[2].Content, "nameserver 1.1.1.1") {
		t.Fatalf("upstream resolver file = %#v", files[2])
	}
}

func TestRenderInitialRequiresTenantDNSSuffix(t *testing.T) {
	_, err := RenderInitial("", "10.248.0.53")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRenderTenantIncludesMachineRecords(t *testing.T) {
	files, err := RenderTenant("acme", "10.248.0.53", []meta.Machine{
		{Project: "default", Name: "codex", PrivateIP: "10.248.0.20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	zone := files[1].Content
	if !strings.Contains(zone, "codex.default.acme. IN A 10.248.0.20") {
		t.Fatalf("zone missing exact machine record: %q", zone)
	}
	if !strings.Contains(zone, "*.codex.default.acme. IN A 10.248.0.20") {
		t.Fatalf("zone missing wildcard machine record: %q", zone)
	}
	if strings.Contains(zone, "*.default.acme") {
		t.Fatalf("zone should not contain project-wide wildcard: %q", zone)
	}
}

func TestPlanApply(t *testing.T) {
	result, err := PlanApply(Tenant{
		Tenant:      "acme",
		DNSSuffix:   "acme",
		PrivateCIDR: "10.248.0.0/24",
	}, []meta.Machine{{Project: "default", Name: "codex", PrivateIP: "10.248.0.20"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.DNSAddress != "10.248.0.53" {
		t.Fatalf("DNSAddress = %q", result.DNSAddress)
	}
	if result.RecordCount != 4 {
		t.Fatalf("RecordCount = %d", result.RecordCount)
	}
}
