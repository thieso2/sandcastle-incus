package localtrust

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestPlanInstallFindsManagedProject(t *testing.T) {
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
	plan, err := PlanInstall(context.Background(), scconfig.LoadAdminFromEnv(), project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-alice-myproject",
		Config: configMap,
	}}}, Request{Reference: "alice/myproject"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.IncusProject != "sc-alice-myproject" {
		t.Fatalf("IncusProject = %q", plan.IncusProject)
	}
	if plan.CAVolume != project.CAVolumeName || plan.CertificatePath != project.ProjectCACertPath {
		t.Fatalf("CA location = %s:%s", plan.CAVolume, plan.CertificatePath)
	}
	if !strings.Contains(plan.Warning, "mint certificates") {
		t.Fatalf("Warning = %q", plan.Warning)
	}
}

func TestPlanInstallSupportsProjectShorthandWithOwner(t *testing.T) {
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
	admin := scconfig.LoadAdminFromEnv()
	admin.Owner = "alice"
	plan, err := PlanInstall(context.Background(), admin, project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-alice-myproject",
		Config: configMap,
	}}}, Request{Reference: "myproject"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Reference != "alice/myproject" {
		t.Fatalf("Reference = %q", plan.Reference)
	}
}

func TestPlanInstallRejectsMissingProject(t *testing.T) {
	_, err := PlanInstall(context.Background(), scconfig.LoadAdminFromEnv(), project.MemoryStore{}, Request{Reference: "alice/missing"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFileStoreInstallAndUninstall(t *testing.T) {
	dir := t.TempDir()
	store := FileStore{Dir: dir}
	plan := Plan{Reference: "alice/myproject", TrustName: "Sandcastle alice/myproject project CA"}
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
		Reference: "alice/myproject",
		TrustName: "Sandcastle alice/myproject project CA",
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
	plan := Plan{Reference: "alice/myproject", TrustName: "Sandcastle alice/myproject project CA"}
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

func TestCommandStoreUninstallLinuxRemovesTrustFileAndUpdates(t *testing.T) {
	dir := t.TempDir()
	plan := Plan{Reference: "alice/myproject", TrustName: "Sandcastle alice/myproject project CA"}
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
	plan := Plan{Reference: "alice/myproject", TrustName: "Sandcastle alice/myproject project CA"}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "PRIVATE KEY") {
		t.Fatalf("payload leaked key material: %s", payload)
	}
}
