package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/infra"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

const infrastructureKind = "infrastructure"

func TestCleanupDisposableResourcesE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e cleanup")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}
	runToken, err := cleanupRunToken(e2eConfig)
	if err != nil {
		t.Fatal(err)
	}

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
	ctx := context.Background()
	store := incusx.NewProjectStore(e2eConfig.Remote)
	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}

	projectDeleter := incusx.NewProjectDeleter(e2eConfig.Remote)
	infraDeleter := incusx.NewInfrastructureDeleter(e2eConfig.Remote)
	deletedProjects := 0
	deletedInfrastructure := 0
	for _, incusProject := range projects {
		switch incusProject.Config[meta.KeyKind] {
		case meta.KindProject:
			if !managedProjectMatchesRun(incusProject, runToken) {
				continue
			}
			managed, err := meta.ParseProjectConfig(incusProject.Config)
			if err != nil {
				t.Fatalf("parse project metadata for cleanup target %s: %v", incusProject.Name, err)
			}
			deletePlan, err := project.PlanDelete(adminConfig, project.DeleteRequest{
				Reference: managed.Owner + "/" + managed.Project,
				Purge:     true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := projectDeleter.DeleteProject(ctx, deletePlan); err != nil {
				t.Fatalf("cleanup project %s: %v", deletePlan.Reference, err)
			}
			deletedProjects++
		case infrastructureKind:
			if !managedInfrastructureMatchesRun(incusProject, runToken) {
				continue
			}
			deletePlan, err := infra.PlanDelete(adminConfig, infra.DeleteRequest{Project: incusProject.Name})
			if err != nil {
				t.Fatal(err)
			}
			if err := infraDeleter.DeleteInfrastructure(ctx, deletePlan); err != nil {
				t.Fatalf("cleanup infrastructure project %s: %v", incusProject.Name, err)
			}
			deletedInfrastructure++
		}
	}
	t.Logf("cleanup run %q removed %d project(s) and %d infrastructure project(s)", runToken, deletedProjects, deletedInfrastructure)
}

func cleanupRunToken(config Config) (string, error) {
	runToken := safeToken(strings.TrimSpace(config.RunID))
	if runToken == "" {
		return "", fmt.Errorf("SANDCASTLE_E2E_RUN_ID is required for standalone e2e cleanup")
	}
	if len(runToken) < 8 {
		return "", fmt.Errorf("SANDCASTLE_E2E_RUN_ID %q is too short for standalone e2e cleanup", config.RunID)
	}
	return runToken, nil
}

func managedProjectMatchesRun(incusProject project.IncusProject, runToken string) bool {
	if strings.Contains(incusProject.Name, runToken) {
		return true
	}
	managed, err := meta.ParseProjectConfig(incusProject.Config)
	if err != nil {
		return false
	}
	for _, value := range []string{managed.Owner, managed.Project, managed.Domain} {
		if strings.Contains(value, runToken) {
			return true
		}
	}
	return false
}

func managedInfrastructureMatchesRun(incusProject project.IncusProject, runToken string) bool {
	if strings.Contains(incusProject.Name, runToken) {
		return true
	}
	return strings.Contains(incusProject.Config[meta.KeyName], runToken)
}

func TestCleanupRunTokenRequiresExplicitLongRunID(t *testing.T) {
	if _, err := cleanupRunToken(Config{}); err == nil {
		t.Fatal("expected missing run id error")
	}
	if _, err := cleanupRunToken(Config{RunID: "short"}); err == nil {
		t.Fatal("expected short run id error")
	}
	token, err := cleanupRunToken(Config{RunID: "e2e-20260520-120000"})
	if err != nil {
		t.Fatal(err)
	}
	if token != "e2e-20260520-120000" {
		t.Fatalf("token = %q", token)
	}
}

func TestCleanupProjectSelectionMatchesOnlyRunID(t *testing.T) {
	config, err := meta.ProjectConfig(meta.Project{
		Owner:           "owner-e2e-20260520-120000",
		Project:         "project-e2e-20260520-120000",
		Domain:          "project-e2e-20260520-120000.e2e.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !managedProjectMatchesRun(project.IncusProject{Name: "sc-owner-e2e-20260520-120000-project", Config: config}, "e2e-20260520-120000") {
		t.Fatal("expected project cleanup match")
	}
	if managedProjectMatchesRun(project.IncusProject{Name: "sc-owner-other-project", Config: config}, "e2e-19990101-000000") {
		t.Fatal("unexpected project cleanup match")
	}
}

func TestCleanupInfrastructureSelectionMatchesOnlyRunID(t *testing.T) {
	project := project.IncusProject{
		Name: "sc-infra-e2e-20260520-120000",
		Config: map[string]string{
			meta.KeyKind:    infrastructureKind,
			meta.KeyVersion: "1",
			meta.KeyName:    "sc-infra-e2e-20260520-120000",
		},
	}
	if !managedInfrastructureMatchesRun(project, "e2e-20260520-120000") {
		t.Fatal("expected infrastructure cleanup match")
	}
	if managedInfrastructureMatchesRun(project, "e2e-19990101-000000") {
		t.Fatal("unexpected infrastructure cleanup match")
	}
}
