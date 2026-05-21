package dns

import (
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
)

func TestRenderInitial(t *testing.T) {
	files, err := RenderInitial("Acme.", "10.248.0.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("len(files) = %d, want 4", len(files))
	}
	corefile := fileByPath(t, files, "/etc/coredns/Corefile")
	if corefile.Path != "/etc/coredns/Corefile" {
		t.Fatalf("Corefile path = %q", corefile.Path)
	}
	if !strings.Contains(corefile.Content, "acme:53") {
		t.Fatalf("Corefile content = %q", corefile.Content)
	}
	if !strings.Contains(corefile.Content, "forward . /etc/coredns/upstream-resolv.conf") {
		t.Fatalf("Corefile missing upstream forwarder: %q", corefile.Content)
	}
	if !strings.Contains(corefile.Content, "force_tcp") {
		t.Fatalf("Corefile missing TCP upstream forwarding: %q", corefile.Content)
	}
	zoneFile := fileByPath(t, files, "/etc/coredns/zones/db.acme")
	if zoneFile.Path != "/etc/coredns/zones/db.acme" {
		t.Fatalf("zone path = %q", zoneFile.Path)
	}
	if !strings.Contains(zoneFile.Content, "ns IN A 10.248.0.3") {
		t.Fatalf("zone content = %q", zoneFile.Content)
	}
	if strings.Contains(zoneFile.Content, "*") {
		t.Fatalf("initial zone should not contain wildcards: %q", zoneFile.Content)
	}
	resolver := fileByPath(t, files, "/etc/resolv.conf")
	if resolver.Content != "nameserver 127.0.0.1\n" {
		t.Fatalf("self resolver file = %#v", resolver)
	}
	upstream := fileByPath(t, files, "/etc/coredns/upstream-resolv.conf")
	if !strings.Contains(upstream.Content, "nameserver 1.1.1.1") {
		t.Fatalf("upstream resolver file = %#v", upstream)
	}
}

func TestRenderInitialRequiresTenantDNSSuffix(t *testing.T) {
	_, err := RenderInitial("", "10.248.0.3")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRenderTenantIncludesMachineRecords(t *testing.T) {
	files, err := RenderTenant("acme", "10.248.0.3", []meta.Machine{
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
	if strings.Contains(zone, "codex.default IN A") || strings.Contains(zone, "*.codex.default IN A") {
		t.Fatalf("zone should not contain relative short machine records: %q", zone)
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
	if result.DNSAddress != "10.248.0.3" {
		t.Fatalf("DNSAddress = %q", result.DNSAddress)
	}
	if result.RecordCount != 4 {
		t.Fatalf("RecordCount = %d", result.RecordCount)
	}
}

func fileByPath(t *testing.T, files []File, path string) File {
	t.Helper()
	for _, file := range files {
		if file.Path == path {
			return file
		}
	}
	t.Fatalf("missing file %s in %#v", path, files)
	return File{}
}
