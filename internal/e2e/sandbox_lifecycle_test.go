package e2e

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/images"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
)

func TestSandboxLifecycleE2E(t *testing.T) {
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
	sandboxName := safeProjectName("box-" + runID)
	ref := owner + "/" + name
	sandboxRef := ref + "/" + sandboxName
	baseAlias := "sandcastle/base:" + safeToken(runID) + "-sandbox"
	aiAlias := "sandcastle/ai:" + safeToken(runID) + "-sandbox"
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
		SSHPublicKey:  e2eConfig.SSHPublicKey,
		OccupiedCIDRs: project.OccupiedCIDRs(existing),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateProject(ctx, createProjectPlan); err != nil {
		t.Fatal(err)
	}

	sandboxCreator := incusx.NewSandboxCreator(e2eConfig.Remote)
	createSandboxPlan, err := sandbox.PlanCreate(ctx, adminConfig, store, incusx.NewHostOverrideManager(e2eConfig.Remote), sandbox.CreateRequest{
		Reference: sandboxRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sandboxCreator.CreateSandbox(ctx, createSandboxPlan); err != nil {
		t.Fatal(err)
	}

	projectServer := server.UseProject(createProjectPlan.IncusProject)
	assertInstanceExists(t, projectServer, createSandboxPlan.InstanceName)
	hostname := sandboxName + "." + createProjectPlan.Domain
	assertSandboxIngressFiles(t, projectServer, createSandboxPlan.InstanceName, hostname, createSandboxPlan.AppPort)
	startSandboxHTTPApp(t, projectServer, createSandboxPlan.InstanceName, createSandboxPlan.AppPort, "sandcastle-app-3000")
	assertSandboxCaddyProxy(t, projectServer, createSandboxPlan.InstanceName, hostname, "sandcastle-app-3000")

	portPlan, err := sandbox.PlanSetPort(ctx, adminConfig, store, sandbox.PortSetRequest{
		Reference: sandboxRef,
		AppPort:   5173,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := incusx.NewSandboxPortSetter(e2eConfig.Remote).SetAppPort(ctx, portPlan); err != nil {
		t.Fatal(err)
	}
	assertSandboxIngressFiles(t, projectServer, createSandboxPlan.InstanceName, hostname, 5173)
	startSandboxHTTPApp(t, projectServer, createSandboxPlan.InstanceName, 5173, "sandcastle-app-5173")
	assertSandboxCaddyProxy(t, projectServer, createSandboxPlan.InstanceName, hostname, "sandcastle-app-5173")

	controller := incusx.NewSandboxController(e2eConfig.Remote)
	for _, action := range []sandbox.Action{sandbox.ActionStop, sandbox.ActionStart, sandbox.ActionRestart, sandbox.ActionRemove} {
		plan, err := sandbox.PlanLifecycle(ctx, adminConfig, store, sandbox.LifecycleRequest{
			Reference: sandboxRef,
			Action:    action,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := controller.ApplyLifecycle(ctx, plan); err != nil {
			t.Fatalf("%s sandbox: %v", action, err)
		}
	}
	if _, _, err := projectServer.GetInstance(createSandboxPlan.InstanceName); !api.StatusErrorCheck(err, http.StatusNotFound) {
		t.Fatalf("expected sandbox %s to be removed, err = %v", createSandboxPlan.InstanceName, err)
	}
}

func startSandboxHTTPApp(t *testing.T, server interface {
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}, instance string, port int, body string) {
	t.Helper()
	command := []string{"/bin/sh", "-lc", fmt.Sprintf(
		"install -d /tmp/sandcastle-app-%d && printf %%s %s >/tmp/sandcastle-app-%d/index.html && cd /tmp/sandcastle-app-%d && nohup python3 -m http.server %d --bind 127.0.0.1 >/tmp/sandcastle-app-%d.log 2>&1 & for i in $(seq 1 50); do curl -fsS http://127.0.0.1:%d/ >/dev/null 2>&1 && exit 0; sleep 0.1; done; exit 1",
		port,
		shellQuote(body),
		port,
		port,
		port,
		port,
		port,
	)}
	_ = execInstanceOutput(t, server, instance, command)
}

func assertSandboxCaddyProxy(t *testing.T, server interface {
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}, instance string, hostname string, want string) {
	t.Helper()
	output := execInstanceOutput(t, server, instance, []string{
		"curl", "-ksS", "--resolve", hostname + ":443:127.0.0.1", "https://" + hostname + "/",
	})
	if !strings.Contains(output, want) {
		t.Fatalf("sandbox Caddy proxy output = %q, want %q", output, want)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func syncImageAlias(t *testing.T, ctx context.Context, manager incusx.ImageManager, adminConfig config.Admin, source string) {
	t.Helper()
	plan, err := images.PlanSync(adminConfig, images.SyncRequest{SourceRef: source})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SyncImage(ctx, plan); err != nil {
		t.Fatal(err)
	}
}

func cleanupImageAlias(t *testing.T, e2eConfig Config, server interface {
	DeleteImageAlias(name string) error
}, alias string) func() {
	return func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable image alias %s", alias)
			return
		}
		if err := server.DeleteImageAlias(alias); err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			t.Logf("cleanup failed for image alias %s: %v", alias, err)
		}
	}
}
