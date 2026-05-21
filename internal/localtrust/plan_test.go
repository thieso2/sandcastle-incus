package localtrust

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestPlanInstallFindsManagedTenant(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanInstall(context.Background(), scconfig.LoadAdminFromEnv(), tenant.MemoryStore{Projects: []tenant.IncusProject{{
		Name:   "sc-acme",
		Config: configMap,
	}}}, Request{Reference: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.IncusProject != "sc-acme" {
		t.Fatalf("IncusProject = %q", plan.IncusProject)
	}
	if plan.CAVolume != tenant.CAVolumeName || plan.CertificatePath != tenant.TenantCACertPath {
		t.Fatalf("CA location = %s:%s", plan.CAVolume, plan.CertificatePath)
	}
	if !strings.Contains(plan.Warning, "mint certificates") {
		t.Fatalf("Warning = %q", plan.Warning)
	}
}

func TestPlanInstallSupportsCurrentTenant(t *testing.T) {
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	admin := scconfig.LoadAdminFromEnv()
	admin.Tenant = "acme"
	plan, err := PlanInstall(context.Background(), admin, tenant.MemoryStore{Projects: []tenant.IncusProject{{
		Name:   "sc-acme",
		Config: configMap,
	}}}, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Reference != "acme" {
		t.Fatalf("Reference = %q", plan.Reference)
	}
}

func TestPlanInstallRejectsMissingTenant(t *testing.T) {
	_, err := PlanInstall(context.Background(), scconfig.LoadAdminFromEnv(), tenant.MemoryStore{}, Request{Reference: "missing"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFileStoreInstallAndUninstall(t *testing.T) {
	dir := t.TempDir()
	store := FileStore{Dir: dir}
	plan := Plan{Reference: "acme", TrustName: "Sandcastle acme tenant CA"}
	result, err := store.InstallCA(context.Background(), plan, []byte("CERT"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "install" {
		t.Fatalf("Action = %q", result.Action)
	}
	content, err := os.ReadFile(filepath.Join(dir, CertFilename(plan)))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "CERT" {
		t.Fatalf("content = %q", content)
	}
	result, err = store.UninstallCA(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "uninstall" {
		t.Fatalf("Action = %q", result.Action)
	}
	if _, err := os.Stat(filepath.Join(dir, CertFilename(plan))); !os.IsNotExist(err) {
		t.Fatalf("expected cert removal, stat err = %v", err)
	}
}

func TestCommandStoreRejectsEmptyCA(t *testing.T) {
	_, err := (CommandStore{GOOS: "linux", LinuxDir: t.TempDir()}).InstallCA(context.Background(), Plan{
		Reference: "acme",
		TrustName: "Sandcastle acme tenant CA",
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "certificate is empty") {
		t.Fatalf("error = %q", err)
	}
}

func TestCommandStoreInstallLinuxCreatesTrustDirectoryAndUpdates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca-certificates")
	var commands []string
	store := CommandStore{
		GOOS:     "linux",
		LinuxDir: dir,
		RunCommand: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			commands = append(commands, name)
			return []byte("ok"), nil
		},
	}
	plan := Plan{Reference: "acme", TrustName: "Sandcastle acme tenant CA"}
	result, err := store.InstallCA(context.Background(), plan, []byte("CERT"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Platform != "linux" || result.Action != "install" {
		t.Fatalf("result = %#v", result)
	}
	content, err := os.ReadFile(filepath.Join(dir, CertFilename(plan)))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "CERT" {
		t.Fatalf("content = %q", content)
	}
	if len(commands) != 1 || commands[0] != "update-ca-certificates" {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestCommandStoreInstallDarwinUsesSudoWhenNotRoot(t *testing.T) {
	var commandName string
	var commandArgs []string
	store := CommandStore{
		GOOS:         "darwin",
		EffectiveUID: func() int { return 501 },
		RunCommand: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			commandName = name
			commandArgs = append([]string{}, args...)
			return []byte("ok"), nil
		},
	}
	plan := Plan{Reference: "acme", TrustName: "Sandcastle acme tenant CA"}
	result, err := store.InstallCA(context.Background(), plan, []byte("CERT"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Platform != "darwin" || result.Action != "install" {
		t.Fatalf("result = %#v", result)
	}
	if commandName != "sudo" {
		t.Fatalf("commandName = %q", commandName)
	}
	if len(commandArgs) < 2 || commandArgs[0] != "security" || commandArgs[1] != "add-trusted-cert" {
		t.Fatalf("commandArgs = %#v", commandArgs)
	}
	if !slices.Contains(commandArgs, "/Library/Keychains/System.keychain") {
		t.Fatalf("commandArgs missing system keychain: %#v", commandArgs)
	}
}

func TestCommandStoreInstallDarwinUsesSecurityDirectlyWhenRoot(t *testing.T) {
	var commandName string
	var commandArgs []string
	store := CommandStore{
		GOOS:         "darwin",
		EffectiveUID: func() int { return 0 },
		RunCommand: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			commandName = name
			commandArgs = append([]string{}, args...)
			return []byte("ok"), nil
		},
	}
	_, err := store.InstallCA(context.Background(), Plan{
		Reference: "acme",
		TrustName: "Sandcastle acme tenant CA",
	}, []byte("CERT"))
	if err != nil {
		t.Fatal(err)
	}
	if commandName != "security" {
		t.Fatalf("commandName = %q", commandName)
	}
	if len(commandArgs) == 0 || commandArgs[0] != "add-trusted-cert" {
		t.Fatalf("commandArgs = %#v", commandArgs)
	}
}

func TestCommandStoreUninstallLinuxRemovesTrustFileAndUpdates(t *testing.T) {
	dir := t.TempDir()
	plan := Plan{Reference: "acme", TrustName: "Sandcastle acme tenant CA"}
	target := filepath.Join(dir, CertFilename(plan))
	if err := os.WriteFile(target, []byte("CERT"), 0o644); err != nil {
		t.Fatal(err)
	}
	var commands []string
	store := CommandStore{
		GOOS:     "linux",
		LinuxDir: dir,
		RunCommand: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			commands = append(commands, name)
			return []byte("ok"), nil
		},
	}
	result, err := store.UninstallCA(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Platform != "linux" || result.Action != "uninstall" {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected cert removal, stat err = %v", err)
	}
	if len(commands) != 1 || commands[0] != "update-ca-certificates" {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestPlanDoesNotSerializePEM(t *testing.T) {
	plan := Plan{Reference: "acme", TrustName: "Sandcastle acme tenant CA"}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "PRIVATE KEY") {
		t.Fatalf("payload leaked key material: %s", payload)
	}
}
