package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/images"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
)

func TestImageSyncAliasE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}
	source := strings.TrimSpace(e2eConfig.Images.BaseSource)
	if source == "" {
		t.Skip("set SANDCASTLE_E2E_BASE_IMAGE_SOURCE to an already-imported Sandcastle base image or alias")
	}

	runID := e2eConfig.DisposableRunID()
	alias := "sandcastle/base:" + safeToken(runID)
	adminConfig := config.Admin{
		Remote:                e2eConfig.Remote,
		StoragePool:           e2eConfig.StoragePool,
		CIDRPool:              e2eConfig.CIDRPool,
		ProjectPrefix:         config.DefaultProjectPrefix,
		InfrastructureProject: config.DefaultInfrastructureProject,
		Images: config.Images{
			Base: alias,
			AI:   config.DefaultAIImageAlias,
		},
	}
	server, err := e2eInstanceServer(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable image alias %s", alias)
			return
		}
		if err := server.DeleteImageAlias(alias); err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			t.Logf("cleanup failed for image alias %s: %v", alias, err)
		}
	})

	plan, err := images.PlanSync(adminConfig, images.SyncRequest{SourceRef: source})
	if err != nil {
		t.Fatal(err)
	}
	result, err := incusx.NewImageManager(e2eConfig.Remote).SyncImage(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Fingerprint == "" {
		t.Fatal("synced image fingerprint is empty")
	}
	synced, _, err := server.GetImageAlias(alias)
	if err != nil {
		t.Fatal(err)
	}
	if synced.Target != result.Fingerprint {
		t.Fatalf("alias target = %q, want %q", synced.Target, result.Fingerprint)
	}
}
