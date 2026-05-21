package e2e

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestLogTenantDiagnosticsDoesNotFailWithoutMatches(t *testing.T) {
	logTenantDiagnostics(t, context.Background(), tenant.MemoryStore{}, "missing")
}

func TestLogTenantDiagnosticsWithMatch(t *testing.T) {
	store := diagnosticTenantStore(t)
	logTenantDiagnostics(t, context.Background(), store, "e2e-test")
}

func TestTenantDiagnosticLinesIncludeTopology(t *testing.T) {
	store := diagnosticTenantStore(t)
	summaries, err := tenant.List(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	lines := tenantDiagnosticLines(context.Background(), summaries, fakeDiagnosticTopologyStore{
		topology: tenant.Topology{
			PrivateNetworkPresent: true,
			TailscaleInstance:     "sc-tenant-e2e-test",
			DurableVolumes: map[string]bool{
				tenant.HomeVolumeName: true,
				tenant.CAVolumeName:   true,
			},
			Sidecars: map[string]tenant.SidecarStatus{
				"sc-tenant-e2e-test": {Present: true, Running: true, Status: "Running"},
				tenant.DNSName:       {Present: true, Running: false, Status: "Stopped"},
			},
			DiagnosticFiles: []tenant.DiagnosticFile{
				{Instance: tenant.DNSName, Path: "/etc/coredns/Corefile", Content: ".:53 {\n  errors\n}"},
				{Instance: tenant.DNSName, Path: "/etc/coredns/zones/db.tenant-e2e-test", Error: "missing"},
			},
		},
	}, "e2e-test")
	if len(lines) != 1 {
		t.Fatalf("lines = %#v, want one diagnostic line", lines)
	}
	for _, want := range []string{
		"topology:",
		"network:sc-private=ok",
		"volume:sc-home=ok",
		"volume:sc-workspace=missing",
		"sidecar:sc-tenant-e2e-test=ok(Running)",
		"sidecar:sc-dns=stopped(Stopped)",
		"files:",
		"sc-dns:/etc/coredns/Corefile",
		"  errors",
		"sc-dns:/etc/coredns/zones/db.tenant-e2e-test error=missing",
	} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("diagnostic line missing %q:\n%s", want, lines[0])
		}
	}
}

func TestTenantDiagnosticLinesIncludeRedactedTailscaleState(t *testing.T) {
	config, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "tenant-e2e-test",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
		Tailscale: meta.Tailscale{
			State:            "Running",
			Tailnet:          "dev.example",
			Hostname:         "sc-tenant.tailnet.example",
			AdvertisedRoutes: []string{"10.248.0.0/24"},
			TailscaleIPs:     []string{"100.80.12.34", "fd7a:115c:a1e0::1"},
			LastCheckedAt:    "2026-05-20T12:00:00Z",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := tenant.List(context.Background(), tenant.MemoryStore{Projects: []tenant.IncusProject{{
		Name:   "sc-tenant-e2e-test",
		Config: config,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	lines := tenantDiagnosticLines(context.Background(), summaries, nil, "e2e-test")
	if len(lines) != 1 {
		t.Fatalf("lines = %#v, want one diagnostic line", lines)
	}
	for _, want := range []string{
		"tailscale:",
		"state=Running",
		"tailnet=dev.example",
		"hostname=sc-tenant.tailnet.example",
		"routes=10.248.0.0/24",
		"ips=2",
		"lastCheckedAt=2026-05-20T12:00:00Z",
	} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("diagnostic line missing %q:\n%s", want, lines[0])
		}
	}
}

func TestTenantDiagnosticLinesRedactTailscaleSecrets(t *testing.T) {
	config, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "tenant-e2e-test",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
		Tailscale: meta.Tailscale{
			State:    "NeedsLogin",
			Tailnet:  "https://login.tailscale.com/a/secret-token",
			Hostname: "tskey-auth-secret",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := tenant.List(context.Background(), tenant.MemoryStore{Projects: []tenant.IncusProject{{
		Name:   "sc-tenant-e2e-test",
		Config: config,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	lines := tenantDiagnosticLines(context.Background(), summaries, nil, "e2e-test")
	if len(lines) != 1 {
		t.Fatalf("lines = %#v, want one diagnostic line", lines)
	}
	for _, forbidden := range []string{"login.tailscale.com", "secret-token", "tskey-auth-secret"} {
		if strings.Contains(lines[0], forbidden) {
			t.Fatalf("diagnostic line leaked %q:\n%s", forbidden, lines[0])
		}
	}
	if !strings.Contains(lines[0], "tailnet=<redacted>") || !strings.Contains(lines[0], "hostname=<redacted>") {
		t.Fatalf("diagnostic line missing redaction markers:\n%s", lines[0])
	}
}

func TestTenantDiagnosticLinesIncludeTopologyErrors(t *testing.T) {
	store := diagnosticTenantStore(t)
	summaries, err := tenant.List(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	lines := tenantDiagnosticLines(context.Background(), summaries, fakeDiagnosticTopologyStore{err: errors.New("boom")}, "e2e-test")
	if len(lines) != 1 {
		t.Fatalf("lines = %#v, want one diagnostic line", lines)
	}
	if !strings.Contains(lines[0], "topology: error=boom") {
		t.Fatalf("diagnostic line missing topology error:\n%s", lines[0])
	}
}

func TestTenantDiagnosticLinesMatchRunIDInDNSSuffix(t *testing.T) {
	config, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "tenant-e2e-domain-only",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := tenant.List(context.Background(), tenant.MemoryStore{Projects: []tenant.IncusProject{{
		Name:   "sc-tenant-e2e-domain-only",
		Config: config,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	lines := tenantDiagnosticLines(context.Background(), summaries, nil, "e2e-domain-only")
	if len(lines) != 1 {
		t.Fatalf("lines = %#v, want one diagnostic line", lines)
	}
	if !strings.Contains(lines[0], "dnsSuffix=tenant-e2e-domain-only") {
		t.Fatalf("diagnostic line missing DNS suffix:\n%s", lines[0])
	}
}

func TestTenantDiagnosticLinesRequireRunID(t *testing.T) {
	store := diagnosticTenantStore(t)
	summaries, err := tenant.List(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	lines := tenantDiagnosticLines(context.Background(), summaries, nil, "")
	if len(lines) != 0 {
		t.Fatalf("lines = %#v, want no diagnostics for empty run id", lines)
	}
	lines = tenantDiagnosticLines(context.Background(), summaries, nil, "   ")
	if len(lines) != 0 {
		t.Fatalf("lines = %#v, want no diagnostics for blank run id", lines)
	}
}

func TestLocalDNSDiagnosticLinesIncludeMatchingState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "dns.yaml")
	content := "tenants:\n" +
		"- tenant: tenant-e2e-test\n" +
		"  dnsSuffix: tenant-e2e-test\n" +
		"  dnsEndpoint:\n" +
		"    ip: 127.0.0.1\n" +
		"    port: 53541\n" +
		"  resolver:\n" +
		"    listen: 127.0.0.1:53540\n" +
		"- tenant: other\n" +
		"  dnsSuffix: other\n" +
		"  dnsEndpoint:\n" +
		"    ip: 127.0.0.1\n" +
		"    port: 53542\n" +
		"  resolver:\n" +
		"    listen: 127.0.0.1:53540\n"
	if err := os.WriteFile(statePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	lines, err := localDNSDiagnosticLines(statePath, "e2e-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("lines = %#v, want one local DNS diagnostic line", lines)
	}
	for _, want := range []string{
		"local-dns:",
		"tenant-e2e-test",
		"dnsSuffix=tenant-e2e-test",
		"endpoint=127.0.0.1:53541",
		"resolver=127.0.0.1:53540",
	} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("local DNS diagnostic line missing %q:\n%s", want, lines[0])
		}
	}
}

func diagnosticTenantStore(t *testing.T) tenant.MemoryStore {
	t.Helper()
	config, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "tenant-e2e-test",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	return tenant.MemoryStore{Projects: []tenant.IncusProject{{
		Name:   "sc-tenant-e2e-test",
		Config: config,
	}}}
}

type fakeDiagnosticTopologyStore struct {
	topology tenant.Topology
	err      error
}

func (s fakeDiagnosticTopologyStore) GetTopology(ctx context.Context, request tenant.TopologyRequest) (tenant.Topology, error) {
	if s.err != nil {
		return tenant.Topology{}, s.err
	}
	return s.topology, nil
}
