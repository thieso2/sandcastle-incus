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
	"github.com/thieso2/sandcastle-incus/internal/project"
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
