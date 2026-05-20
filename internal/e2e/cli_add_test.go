package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/cli"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
)

func TestCLIAddDetachE2E(t *testing.T) {
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
	sandboxName := safeProjectName("cli-" + runID)
	ref := owner + "/" + name
	sandboxRef := ref + "/" + sandboxName
	baseAlias := "sandcastle/base:" + safeToken(runID) + "-cli"
	aiAlias := "sandcastle/ai:" + safeToken(runID) + "-cli"
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

	t.Setenv("SANDCASTLE_REMOTE", e2eConfig.Remote)
	t.Setenv("SANDCASTLE_STORAGE_POOL", e2eConfig.StoragePool)
	t.Setenv("SANDCASTLE_CIDR_POOL", e2eConfig.CIDRPool)
	t.Setenv("SANDCASTLE_PROJECT_PREFIX", config.DefaultProjectPrefix)
	t.Setenv("SANDCASTLE_INFRA_PROJECT", config.DefaultInfrastructureProject)
	t.Setenv("SANDCASTLE_BASE_IMAGE", baseAlias)
	t.Setenv("SANDCASTLE_AI_IMAGE", aiAlias)
	if exitCode := cli.Execute("sandcastle", []string{
		"add", sandboxRef,
		"--detach",
		"--template", "base",
		"--home-dir", "shared-home",
		"--workspace-dir", ".",
	}); exitCode != 0 {
		t.Fatalf("sandcastle add --detach --template base exit code = %d", exitCode)
	}

	projectServer := server.UseProject(createProjectPlan.IncusProject)
	instanceName := "sc-" + sandboxName
	assertInstanceExists(t, projectServer, instanceName)
	instance, _, err := projectServer.GetInstance(instanceName)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Devices["home"]["source"] != project.HomeVolumeName+"/shared-home" {
		t.Fatalf("home source = %q", instance.Devices["home"]["source"])
	}
	if instance.Devices["workspace"]["source"] != project.WorkspaceVolumeName+"/." {
		t.Fatalf("workspace source = %q", instance.Devices["workspace"]["source"])
	}
	assertSandboxIngressFiles(t, projectServer, instanceName, sandboxName+"."+createProjectPlan.Domain, sandbox.DefaultAppPort)
}
