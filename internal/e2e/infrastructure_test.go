package e2e

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/infra"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

func TestDisposableInfrastructureCreateAndDelete(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}
	sandcastleBin := strings.TrimSpace(e2eConfig.SandcastleBin)
	if sandcastleBin == "" {
		sandcastleBin = buildSandcastleForE2E(t)
	}
	t.Setenv("SANDCASTLE_BIN", sandcastleBin)

	ctx := context.Background()
	runID := e2eConfig.DisposableRunID()
	infraProject := safeInfrastructureProject("sc-infra-" + runID)
	adminConfig := config.Admin{
		Remote:                e2eConfig.Remote,
		StoragePool:           e2eConfig.StoragePool,
		CIDRPool:              e2eConfig.CIDRPool,
		ProjectPrefix:         config.DefaultProjectPrefix,
		InfrastructureProject: infraProject,
		Images: config.Images{
			Base: config.DefaultBaseImageAlias,
			AI:   config.DefaultAIImageAlias,
		},
	}
	creator := incusx.NewInfrastructureCreator(e2eConfig.Remote)
	deleter := incusx.NewInfrastructureDeleter(e2eConfig.Remote)
	deletePlan, err := infra.PlanDelete(adminConfig, infra.DeleteRequest{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable infrastructure project %s", infraProject)
			return
		}
		if err := deleter.DeleteInfrastructure(ctx, deletePlan); err != nil {
			t.Logf("cleanup failed for infrastructure project %s: %v", infraProject, err)
		}
	})

	createPlan, err := infra.PlanCreate(adminConfig, infra.CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateInfrastructure(ctx, createPlan); err != nil {
		t.Fatal(err)
	}

	server, err := e2eInstanceServer(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}
	projectServer := server.UseProject(infraProject)
	assertInstanceExists(t, projectServer, route.InfrastructureCaddyName)
	assertInstanceExists(t, projectServer, infra.RouteBrokerName)

	if err := deleter.DeleteInfrastructure(ctx, deletePlan); err != nil {
		t.Fatal(err)
	}
}

func buildSandcastleForE2E(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sandcastle")
	command := exec.Command("go", "build", "-o", path, "./cmd/sandcastle")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build sandcastle e2e binary: %v\n%s", err, strings.TrimSpace(string(output)))
	}
	return path
}

func e2eInstanceServer(remote string) (incus.InstanceServer, error) {
	loaded, err := cliconfig.LoadConfig("")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(remote) == "" {
		remote = loaded.DefaultRemote
	}
	return loaded.GetInstanceServer(remote)
}

func assertInstanceExists(t *testing.T, server incus.InstanceServer, name string) {
	t.Helper()
	if _, _, err := server.GetInstance(name); err != nil {
		t.Fatalf("expected instance %s: %v", name, err)
	}
}

func safeInfrastructureProject(value string) string {
	value = safeToken(value)
	if len(value) > 50 {
		value = value[:50]
	}
	return strings.Trim(value, "-")
}
