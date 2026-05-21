package localdns

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestPlanInstallUsesTenantDNSRoleAddress(t *testing.T) {
	t.Setenv("SANDCASTLE_LOCAL_DNS_STATE", filepath.Join(t.TempDir(), "dns.yaml"))
	t.Setenv("SANDCASTLE_RESOLVER_DIR", t.TempDir())
	plan, err := PlanInstall(context.Background(), scconfig.LoadAdminFromEnv(), storeForTest(t), Request{Reference: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.DNSEndpoint != "10.248.0.53:53" {
		t.Fatalf("DNSEndpoint = %q", plan.DNSEndpoint)
	}
	if plan.Listen != "127.0.0.1:53541" {
		t.Fatalf("Listen = %q", plan.Listen)
	}
	if !strings.HasSuffix(plan.ResolverPath, "acme") {
		t.Fatalf("ResolverPath = %q", plan.ResolverPath)
	}
}

func TestPlanInstallSupportsCurrentTenant(t *testing.T) {
	admin := scconfig.LoadAdminFromEnv()
	admin.Tenant = "acme"
	t.Setenv("SANDCASTLE_LOCAL_DNS_STATE", filepath.Join(t.TempDir(), "dns.yaml"))
	t.Setenv("SANDCASTLE_RESOLVER_DIR", t.TempDir())
	plan, err := PlanInstall(context.Background(), admin, storeForTest(t), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Reference != "acme" {
		t.Fatalf("Reference = %q", plan.Reference)
	}
}

func TestFileManagerInstallRefreshAndUninstall(t *testing.T) {
	dir := t.TempDir()
	plan := Plan{
		Reference:    "acme",
		DNSSuffix:    "acme",
		DNSEndpoint:  "10.248.0.53:53",
		Listen:       "127.0.0.1:53541",
		StatePath:    filepath.Join(dir, "state", "dns.yaml"),
		ResolverPath: filepath.Join(dir, "resolver", "acme"),
	}
	manager := FileManager{}
	result, err := manager.Install(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "install" {
		t.Fatalf("Action = %q", result.Action)
	}
	state, err := os.ReadFile(plan.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), "dnsEndpoint:") || !strings.Contains(string(state), "10.248.0.53") {
		t.Fatalf("state = %s", state)
	}
	resolver, err := os.ReadFile(plan.ResolverPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(resolver) != "nameserver 127.0.0.1\nport 53541\n" {
		t.Fatalf("resolver = %q", resolver)
	}
	plan.DNSEndpoint = "10.248.1.53:53"
	result, err = manager.Refresh(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "refresh" {
		t.Fatalf("Action = %q", result.Action)
	}
	state, err = os.ReadFile(plan.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), "10.248.1.53") || strings.Contains(string(state), "10.248.0.53") {
		t.Fatalf("state = %s", state)
	}
	result, err = manager.Uninstall(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "uninstall" {
		t.Fatalf("Action = %q", result.Action)
	}
	if _, err := os.Stat(plan.ResolverPath); !os.IsNotExist(err) {
		t.Fatalf("expected resolver removal, stat err = %v", err)
	}
}

func TestFileManagerRunsLinuxResolverSyncCommands(t *testing.T) {
	t.Setenv("SANDCASTLE_RESOLVER_DIR", "")
	dir := t.TempDir()
	runner := &recordingServiceRunner{}
	manager := FileManager{Runner: runner}
	plan := Plan{
		Reference:        "acme",
		DNSSuffix:        "acme",
		DNSEndpoint:      "10.248.0.53:53",
		Listen:           "127.0.0.1:53541",
		StatePath:        filepath.Join(dir, "state", "dns.yaml"),
		ResolverPath:     filepath.Join(dir, "resolver", "acme"),
		ResolverStrategy: StrategySystemdResolve,
		ResolverCommands: []Command{{Args: []string{"resolvectl", "dns", "lo", "127.0.0.1:53541"}}},
	}
	if _, err := manager.Install(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	if got := joinArgs(runner.commands[0]); got != "resolvectl dns lo 127.0.0.1:53541" {
		t.Fatalf("dns command = %q", got)
	}
	if got := joinArgs(runner.commands[1]); got != "resolvectl domain lo ~acme" {
		t.Fatalf("domain command = %q", got)
	}
}

func TestFileManagerUninstallSyncsRemainingLinuxResolverDomains(t *testing.T) {
	t.Setenv("SANDCASTLE_RESOLVER_DIR", "")
	dir := t.TempDir()
	runner := &recordingServiceRunner{}
	manager := FileManager{Runner: runner}
	first := linuxResolverPlan(dir, "alpha", "alpha", "10.248.0.53:53")
	second := linuxResolverPlan(dir, "beta", "beta", "10.248.1.53:53")
	if _, err := manager.Install(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	runner.commands = nil
	if _, err := manager.Uninstall(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	if got := joinArgs(runner.commands[1]); got != "resolvectl domain lo ~beta" {
		t.Fatalf("domain command = %q", got)
	}
	runner.commands = nil
	if _, err := manager.Uninstall(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	if got := joinArgs(runner.commands[0]); got != "resolvectl revert lo" {
		t.Fatalf("revert command = %q", got)
	}
}

func linuxResolverPlan(dir string, reference string, domain string, endpoint string) Plan {
	return Plan{
		Reference:        reference,
		DNSSuffix:        domain,
		DNSEndpoint:      endpoint,
		Listen:           "127.0.0.1:53541",
		StatePath:        filepath.Join(dir, "state", "dns.yaml"),
		ResolverPath:     filepath.Join(dir, "resolver", domain),
		ResolverStrategy: StrategySystemdResolve,
		ResolverCommands: []Command{{Args: []string{"resolvectl", "dns", "lo", "127.0.0.1:53541"}}},
	}
}

func storeForTest(t *testing.T) project.MemoryStore {
	t.Helper()
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	return project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-acme",
		Config: configMap,
	}}}
}
