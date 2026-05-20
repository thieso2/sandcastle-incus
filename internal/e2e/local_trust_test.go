package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/localtrust"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

func TestLocalTrustInstallUninstallE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	runID := e2eConfig.DisposableRunID()
	owner := safeProjectName("owner-" + runID)
	name := safeProjectName("trust-" + runID)
	ref := owner + "/" + name
	adminConfig := config.Admin{
		Remote:                e2eConfig.Remote,
		StoragePool:           e2eConfig.StoragePool,
		CIDRPool:              e2eConfig.CIDRPool,
		ProjectPrefix:         config.DefaultProjectPrefix,
		InfrastructureProject: config.DefaultInfrastructureProject,
		Images: config.Images{
			Base: config.DefaultBaseImageAlias,
			AI:   config.DefaultAIImageAlias,
		},
	}

	store := incusx.NewProjectStore(e2eConfig.Remote)
	creator := incusx.NewProjectCreator(e2eConfig.Remote)
	projectDeleter := incusx.NewProjectDeleter(e2eConfig.Remote)
	deletePlan, err := project.PlanDelete(adminConfig, project.DeleteRequest{Reference: ref, Purge: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable project %s", ref)
			return
		}
		if err := projectDeleter.DeleteProject(ctx, deletePlan); err != nil {
			t.Logf("cleanup failed for %s: %v", ref, err)
		}
	})

	existing, err := project.List(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	createPlan, err := project.PlanCreate(adminConfig, project.CreateRequest{
		Reference:     ref,
		Domain:        name + "." + e2eConfig.DomainSuffix,
		OccupiedCIDRs: project.OccupiedCIDRs(existing),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateProject(ctx, createPlan); err != nil {
		t.Fatal(err)
	}

	installPlan, err := localtrust.PlanInstall(ctx, adminConfig, store, localtrust.Request{Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	trustDir := t.TempDir()
	manager := incusx.NewLocalTrustManager(e2eConfig.Remote, localtrust.FileStore{Dir: trustDir})
	result, err := manager.Install(ctx, installPlan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "install" || result.Platform != "file" {
		t.Fatalf("install result = %#v", result)
	}
	target := filepath.Join(trustDir, localtrust.CertFilename(installPlan))
	certPEM, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(certPEM), "BEGIN CERTIFICATE") {
		t.Fatalf("installed trust file = %q", certPEM)
	}

	uninstallPlan, err := localtrust.PlanUninstall(ctx, adminConfig, store, localtrust.Request{Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	result, err = manager.Uninstall(ctx, uninstallPlan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "uninstall" || result.Platform != "file" {
		t.Fatalf("uninstall result = %#v", result)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected trust file removal, stat err = %v", err)
	}
}
