package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/thieso2/sandcastle-incus/internal/cli"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
)

func TestCLIAddDetachE2E(t *testing.T) {
	fixture := setupCLIAddProjectE2E(t, "cli")
	setSandcastleCLIEnv(t, fixture)
	shorthandRef := fixture.ProjectName + "/" + fixture.SandboxName

	if exitCode := cli.Execute("sandcastle", []string{
		"add", shorthandRef,
		"--detach",
		"--template", "base",
		"--home-dir", "shared-home",
		"--workspace-dir", ".",
	}); exitCode != 0 {
		t.Fatalf("sandcastle add --detach --template base exit code = %d", exitCode)
	}

	projectServer := fixture.Server.UseProject(fixture.Project.IncusProject)
	instanceName := "sc-" + fixture.SandboxName
	assertInstanceExists(t, projectServer, instanceName)
	instance, _, err := projectServer.GetInstance(instanceName)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Devices["home"]["source"] != project.HomeVolumeName+"/shared-home" {
		t.Fatalf("home source = %q", instance.Devices["home"]["source"])
	}
	if instance.Devices["home"]["path"] != "/home/"+fixture.Owner {
		t.Fatalf("home path = %q", instance.Devices["home"]["path"])
	}
	if strings.TrimSpace(execInstanceOutput(t, projectServer, instanceName, []string{"id", "-un", fixture.Owner})) != fixture.Owner {
		t.Fatalf("sandbox Linux user %q was not created", fixture.Owner)
	}
	if instance.Devices["workspace"]["source"] != project.WorkspaceVolumeName+"/." {
		t.Fatalf("workspace source = %q", instance.Devices["workspace"]["source"])
	}
	assertSandboxIngressFiles(t, projectServer, instanceName, fixture.SandboxName+"."+fixture.Project.Domain, sandbox.DefaultAppPort)
	sandcastleBin := strings.TrimSpace(fixture.Config.SandcastleBin)
	if sandcastleBin == "" {
		sandcastleBin = buildSandcastleForE2E(t)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, sandcastleBin, "--output", "json", "inspect", shorthandRef)
	command.Env = append(os.Environ(), sandcastleCLIEnv(fixture)...)
	output, err := command.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("sandcastle inspect timed out\n%s", strings.TrimSpace(string(output)))
	}
	if err != nil {
		t.Fatalf("sandcastle inspect: %v\n%s", err, strings.TrimSpace(string(output)))
	}
	var inspect sandbox.InspectResult
	if err := json.Unmarshal(output, &inspect); err != nil {
		t.Fatalf("decode inspect output: %v\n%s", err, strings.TrimSpace(string(output)))
	}
	if inspect.InstanceName != instanceName {
		t.Fatalf("inspect instance = %q, want %q", inspect.InstanceName, instanceName)
	}
	if inspect.Sandbox.PrivateIP == "" || inspect.Sandbox.AppPort != sandbox.DefaultAppPort || inspect.Sandbox.LinuxUser != fixture.Owner || !inspect.Sandbox.Running {
		t.Fatalf("inspect sandbox = %#v", inspect.Sandbox)
	}
}

func TestCLIAddDefaultEnterE2E(t *testing.T) {
	fixture := setupCLIAddProjectE2E(t, "add")
	sandcastleBin := strings.TrimSpace(fixture.Config.SandcastleBin)
	if sandcastleBin == "" {
		sandcastleBin = buildSandcastleForE2E(t)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, sandcastleBin,
		"add", fixture.SandboxRef,
		"--template", "base",
		"--home-dir", "interactive-home",
		"--workspace-dir", ".",
	)
	command.Stdin = strings.NewReader("printf 'sandcastle-add-interactive-ok\\n'; whoami; pwd; exit\n")
	command.Env = append(os.Environ(), sandcastleCLIEnv(fixture)...)
	output, err := command.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("sandcastle add default enter timed out\n%s", strings.TrimSpace(string(output)))
	}
	if err != nil {
		t.Fatalf("sandcastle add default enter: %v\n%s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "sandcastle-add-interactive-ok") {
		t.Fatalf("sandcastle add default enter output missing marker:\n%s", strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), fixture.Owner) {
		t.Fatalf("sandcastle add default enter output missing Linux user %q:\n%s", fixture.Owner, strings.TrimSpace(string(output)))
	}

	projectServer := fixture.Server.UseProject(fixture.Project.IncusProject)
	instanceName := "sc-" + fixture.SandboxName
	assertInstanceExists(t, projectServer, instanceName)
	instance, _, err := projectServer.GetInstance(instanceName)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Devices["home"]["source"] != project.HomeVolumeName+"/interactive-home" {
		t.Fatalf("home source = %q", instance.Devices["home"]["source"])
	}
	if instance.Devices["home"]["path"] != "/home/"+fixture.Owner {
		t.Fatalf("home path = %q", instance.Devices["home"]["path"])
	}
	assertSandboxIngressFiles(t, projectServer, instanceName, fixture.SandboxName+"."+fixture.Project.Domain, sandbox.DefaultAppPort)
}

type cliAddFixture struct {
	Config      Config
	Server      incus.InstanceServer
	Project     project.CreatePlan
	Owner       string
	ProjectName string
	SandboxRef  string
	SandboxName string
	BaseAlias   string
	AIAlias     string
}

func setupCLIAddProjectE2E(t *testing.T, suffix string) cliAddFixture {
	t.Helper()
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
	sandboxName := safeProjectName(suffix + "-" + runID)
	ref := owner + "/" + name
	sandboxRef := ref + "/" + sandboxName
	baseAlias := "sandcastle/base:" + safeToken(runID) + "-" + suffix
	aiAlias := "sandcastle/ai:" + safeToken(runID) + "-" + suffix
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
	registerProjectDiagnostics(t, ctx, store, incusx.NewTopologyStore(e2eConfig.Remote), e2eConfig.StoragePool, runID)
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

	return cliAddFixture{
		Config:      e2eConfig,
		Server:      server,
		Project:     createProjectPlan,
		Owner:       owner,
		ProjectName: name,
		SandboxRef:  sandboxRef,
		SandboxName: sandboxName,
		BaseAlias:   baseAlias,
		AIAlias:     aiAlias,
	}
}

func setSandcastleCLIEnv(t *testing.T, fixture cliAddFixture) {
	t.Helper()
	for _, entry := range sandcastleCLIEnv(fixture) {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("invalid env entry %q", entry)
		}
		t.Setenv(name, value)
	}
}

func sandcastleCLIEnv(fixture cliAddFixture) []string {
	return []string{
		"SANDCASTLE_REMOTE=" + fixture.Config.Remote,
		"SANDCASTLE_OWNER=" + fixture.Owner,
		"SANDCASTLE_STORAGE_POOL=" + fixture.Config.StoragePool,
		"SANDCASTLE_CIDR_POOL=" + fixture.Config.CIDRPool,
		"SANDCASTLE_PROJECT_PREFIX=" + config.DefaultProjectPrefix,
		"SANDCASTLE_INFRA_PROJECT=" + config.DefaultInfrastructureProject,
		"SANDCASTLE_BASE_IMAGE=" + fixture.BaseAlias,
		"SANDCASTLE_AI_IMAGE=" + fixture.AIAlias,
	}
}
