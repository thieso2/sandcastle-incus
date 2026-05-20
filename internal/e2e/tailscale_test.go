package e2e

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/tailscale"
)

func TestTailscaleAttachmentE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}
	baseSource := strings.TrimSpace(e2eConfig.Images.BaseSource)
	authKey := strings.TrimSpace(e2eConfig.Tailscale.AuthKey)
	if baseSource == "" {
		t.Skip("set SANDCASTLE_E2E_BASE_IMAGE_SOURCE to an already-imported Sandcastle base image alias")
	}
	if authKey == "" {
		t.Skip("set SANDCASTLE_E2E_TAILSCALE_AUTHKEY to run real Tailscale attachment e2e tests")
	}

	ctx := context.Background()
	runID := e2eConfig.DisposableRunID()
	owner := safeProjectName("owner-" + runID)
	name := safeProjectName("project-" + runID)
	ref := owner + "/" + name
	baseAlias := "sandcastle/base:" + safeToken(runID) + "-tailscale"
	adminConfig := config.Admin{
		Remote:                e2eConfig.Remote,
		StoragePool:           e2eConfig.StoragePool,
		CIDRPool:              e2eConfig.CIDRPool,
		ProjectPrefix:         config.DefaultProjectPrefix,
		InfrastructureProject: config.DefaultInfrastructureProject,
		Images: config.Images{
			Base: baseAlias,
			AI:   config.DefaultAIImageAlias,
		},
	}

	server, err := e2eInstanceServer(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupImageAlias(t, e2eConfig, server, baseAlias))

	imageManager := incusx.NewImageManager(e2eConfig.Remote)
	syncImageAlias(t, ctx, imageManager, adminConfig, baseSource)

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

	manager := incusx.NewTailscaleManager(e2eConfig.Remote)
	upPlan, err := tailscale.PlanUp(ctx, adminConfig, store, tailscale.UpRequest{
		Reference:     ref,
		AuthKey:       authKey,
		AdvertiseTags: []string{e2eConfig.Tailscale.Tag},
	})
	if err != nil {
		t.Fatal(err)
	}
	downPlan, err := tailscale.PlanDown(ctx, adminConfig, store, tailscale.DownRequest{Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.RunDown(ctx, downPlan, tailscale.RunSession{Stdout: io.Discard, Stderr: io.Discard}); err != nil {
			t.Logf("tailscale down cleanup failed for %s: %v", ref, err)
		}
	})
	if err := manager.RunUp(ctx, upPlan, tailscale.RunSession{Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		t.Fatal(err)
	}

	statusPlan, err := tailscale.PlanStatus(ctx, adminConfig, store, tailscale.StatusRequest{Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	result := waitForTailscaleRunning(t, ctx, manager, statusPlan)
	if !containsString(result.Tailscale.AdvertisedRoutes, createPlan.PrivateCIDR) {
		t.Fatalf("advertised routes = %#v, want %s", result.Tailscale.AdvertisedRoutes, createPlan.PrivateCIDR)
	}
	if result.Tailscale.Tailnet == "" {
		t.Fatalf("expected tailnet in status: %#v", result.Tailscale)
	}
	if len(result.Tailscale.TailscaleIPs) == 0 {
		t.Fatalf("expected tailscale IPs in status: %#v", result.Tailscale)
	}
}

func waitForTailscaleRunning(t *testing.T, ctx context.Context, manager incusx.TailscaleManager, plan tailscale.StatusPlan) tailscale.StatusResult {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last tailscale.StatusResult
	var lastErr error
	for time.Now().Before(deadline) {
		last, lastErr = manager.RunStatus(ctx, plan, tailscale.RunSession{Stderr: io.Discard})
		if lastErr == nil && strings.EqualFold(last.Tailscale.State, "Running") {
			return last
		}
		time.Sleep(time.Second)
	}
	if lastErr != nil {
		t.Fatalf("tailscale status did not become running: %v", lastErr)
	}
	t.Fatalf("tailscale state = %q, want Running", last.Tailscale.State)
	return tailscale.StatusResult{}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
