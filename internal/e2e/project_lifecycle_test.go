package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

func TestDisposableProjectCreateAndPurge(t *testing.T) {
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
	name := safeProjectName("project-" + runID)
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
	deleter := incusx.NewProjectDeleter(e2eConfig.Remote)
	defer func() {
		if t.Failed() {
			logProjectDiagnostics(t, ctx, store, runID)
		}
	}()

	deletePlan, err := project.PlanDelete(adminConfig, project.DeleteRequest{Reference: ref, Purge: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable project %s", ref)
			return
		}
		if err := deleter.DeleteProject(ctx, deletePlan); err != nil {
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

	afterCreate, err := project.List(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if !containsProject(afterCreate, owner, name) {
		t.Fatalf("created project %s was not listed in %#v", ref, afterCreate)
	}
	if err := deleter.DeleteProject(ctx, deletePlan); err != nil {
		t.Fatal(err)
	}
}

func safeProjectName(value string) string {
	value = safeToken(value)
	if len(value) > 40 {
		value = value[:40]
	}
	return strings.Trim(value, "-")
}

func containsProject(projects []project.Summary, owner string, name string) bool {
	for _, summary := range projects {
		if summary.Owner == owner && summary.Name == name {
			return true
		}
	}
	return false
}
