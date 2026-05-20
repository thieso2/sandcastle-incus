package localdns

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

func TestPlanInstallUsesProjectDNSRoleAddress(t *testing.T) {
	t.Setenv("SANDCASTLE_LOCAL_DNS_STATE", filepath.Join(t.TempDir(), "dns.yaml"))
	t.Setenv("SANDCASTLE_RESOLVER_DIR", t.TempDir())
	plan, err := PlanInstall(context.Background(), scconfig.LoadAdminFromEnv(), storeForTest(t), Request{Reference: "alice/myproject"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.DNSEndpoint != "10.248.0.53:53" {
		t.Fatalf("DNSEndpoint = %q", plan.DNSEndpoint)
	}
	if plan.Listen != "127.0.0.1:53541" {
		t.Fatalf("Listen = %q", plan.Listen)
	}
	if !strings.HasSuffix(plan.ResolverPath, "myproject.project-tld") {
		t.Fatalf("ResolverPath = %q", plan.ResolverPath)
	}
}

func TestFileManagerInstallRefreshAndUninstall(t *testing.T) {
	dir := t.TempDir()
	plan := Plan{
		Reference:    "alice/myproject",
		Domain:       "myproject.project-tld",
		DNSEndpoint:  "10.248.0.53:53",
		Listen:       "127.0.0.1:53541",
		StatePath:    filepath.Join(dir, "state", "dns.yaml"),
		ResolverPath: filepath.Join(dir, "resolver", "myproject.project-tld"),
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

func storeForTest(t *testing.T) project.MemoryStore {
	t.Helper()
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	return project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-alice-myproject",
		Config: configMap,
	}}}
}
