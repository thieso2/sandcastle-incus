package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/cli"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestCLIEnterCommandE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}
	baseSource := strings.TrimSpace(e2eConfig.Images.BaseSource)
	aiSource := strings.TrimSpace(e2eConfig.Images.AISource)
	if baseSource == "" || aiSource == "" {
		t.Skip("set SANDCASTLE_E2E_BASE_IMAGE_SOURCE and SANDCASTLE_E2E_AI_IMAGE_SOURCE to already-imported Sandcastle image aliases")
	}

	ctx := context.Background()
	runID := e2eConfig.DisposableRunID()
	owner := safeProjectName("owner-" + runID)
	name := safeProjectName("project-" + runID)
	sandboxName := safeProjectName("enter-" + runID)
	ref := owner + "/" + name
	sandboxRef := ref + "/" + sandboxName
	baseAlias := "sandcastle/base:" + safeToken(runID) + "-enter"
	aiAlias := "sandcastle/ai:" + safeToken(runID) + "-enter"
	adminConfig := config.Admin{
		Remote:                e2eConfig.Remote,
		StoragePool:           e2eConfig.StoragePool,
		CIDRPool:              e2eConfig.CIDRPool,
		ProjectPrefix:         config.DefaultProjectPrefix,
		InfrastructureProject: config.DefaultInfrastructureProject,
		Images: config.Images{
			Base: baseAlias,
			AI:   aiAlias,
		},
	}

	server, err := e2eInstanceServer(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupImageAlias(t, e2eConfig, server, aiAlias))
	t.Cleanup(cleanupImageAlias(t, e2eConfig, server, baseAlias))

	imageManager := incusx.NewImageManager(e2eConfig.Remote)
	syncImageAlias(t, ctx, imageManager, adminConfig, baseSource)
	syncImageAlias(t, ctx, imageManager, adminConfig, aiSource)

	store := incusx.NewProjectStore(e2eConfig.Remote)
	registerProjectDiagnostics(t, ctx, store, incusx.NewTopologyStore(e2eConfig.Remote), runID)
	creator := incusx.NewProjectCreator(e2eConfig.Remote)
	projectDeleter := incusx.NewProjectDeleter(e2eConfig.Remote)
	deletePlan, err := project.PlanDelete(adminConfig, project.DeleteRequest{Reference: ref, Purge: true})
	if err != nil {
		t.Fatal(err)
	}
	// Pre-cleanup: remove any leaked project with the same name from a previous run.
	if err := projectDeleter.DeleteProject(ctx, deletePlan); err != nil {
		t.Logf("pre-cleanup for %s: %v", ref, err)
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
	createProjectPlan, err := project.PlanCreate(adminConfig, project.CreateRequest{
		Reference:     ref,
		Domain:        name + "." + e2eConfig.DomainSuffix,
		OccupiedCIDRs: project.OccupiedCIDRs(existing),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateProject(ctx, createProjectPlan); err != nil {
		t.Fatal(err)
	}

	createSandboxPlan, err := sandbox.PlanCreate(ctx, adminConfig, store, incusx.NewHostOverrideManager(e2eConfig.Remote), sandbox.CreateRequest{Reference: sandboxRef})
	if err != nil {
		t.Fatal(err)
	}
	if err := incusx.NewSandboxCreator(e2eConfig.Remote).CreateSandbox(ctx, createSandboxPlan); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SANDCASTLE_REMOTE", e2eConfig.Remote)
	t.Setenv("SANDCASTLE_STORAGE_POOL", e2eConfig.StoragePool)
	t.Setenv("SANDCASTLE_CIDR_POOL", e2eConfig.CIDRPool)
	t.Setenv("SANDCASTLE_PROJECT_PREFIX", config.DefaultProjectPrefix)
	t.Setenv("SANDCASTLE_INFRA_PROJECT", config.DefaultInfrastructureProject)
	t.Setenv("SANDCASTLE_BASE_IMAGE", baseAlias)
	t.Setenv("SANDCASTLE_AI_IMAGE", aiAlias)
	if exitCode := cli.Execute("sandcastle", []string{"enter", sandboxRef, "pwd"}); exitCode != 0 {
		t.Fatalf("sandcastle enter pwd exit code = %d", exitCode)
	}
}
