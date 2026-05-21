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
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestCLIAddDetachE2E(t *testing.T) {
	fixture := setupCLIAddProjectE2E(t, "cli")
	setSandcastleCLIEnv(t, fixture)
	shorthandRef := fixture.ProjectName + "/" + fixture.SandboxName

	if exitCode := cli.Execute("sandcastle", []string{
		"create", shorthandRef,
		"--detach",
		"--template", "base",
		"--home-dir", "shared-home",
		"--workspace-dir", ".",
		"--container-tools",
	}); exitCode != 0 {
		t.Fatalf("sandcastle create --detach --template base --container-tools exit code = %d", exitCode)
	}

	projectServer := fixture.Server.UseProject(fixture.Project.IncusProject)
	instanceName := fixture.ProjectName + "-" + fixture.SandboxName
	assertInstanceExists(t, projectServer, instanceName)
	instance, _, err := projectServer.GetInstance(instanceName)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Devices["home"]["source"] != project.HomeVolumeName+"/shared-home" {
		t.Fatalf("home source = %q", instance.Devices["home"]["source"])
	}
	if instance.Devices["home"]["path"] != "/home/"+fixture.Tenant {
		t.Fatalf("home path = %q", instance.Devices["home"]["path"])
	}
	if instance.Config["security.nesting"] != "true" {
		t.Fatalf("security.nesting = %q, want true", instance.Config["security.nesting"])
	}
	if _, ok := instance.Config["security.privileged"]; ok {
		t.Fatalf("security.privileged should not be set: %#v", instance.Config)
	}
	if strings.TrimSpace(execInstanceOutput(t, projectServer, instanceName, []string{"id", "-un", fixture.Tenant})) != fixture.Tenant {
		t.Fatalf("sandbox Linux user %q was not created", fixture.Tenant)
	}
	if instance.Devices["workspace"]["source"] != project.WorkspaceVolumeName+"/." {
		t.Fatalf("workspace source = %q", instance.Devices["workspace"]["source"])
	}
	assertSandboxIngressFiles(t, projectServer, instanceName, fixture.SandboxName+"."+fixture.ProjectName+"."+fixture.Project.DNSSuffix, sandbox.DefaultAppPort)
	sandcastleBin := strings.TrimSpace(fixture.Config.SandcastleBin)
	if sandcastleBin == "" {
		sandcastleBin = buildSandcastleForE2E(t)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, sandcastleBin, "--output", "json", "status", shorthandRef)
	command.Env = append(os.Environ(), sandcastleCLIEnv(fixture)...)
	output, err := command.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("sandcastle status timed out\n%s", strings.TrimSpace(string(output)))
	}
	if err != nil {
		t.Fatalf("sandcastle status: %v\n%s", err, strings.TrimSpace(string(output)))
	}
	var status sandbox.InspectResult
	if err := json.Unmarshal(output, &status); err != nil {
		t.Fatalf("decode status output: %v\n%s", err, strings.TrimSpace(string(output)))
	}
	if status.InstanceName != instanceName {
		t.Fatalf("status instance = %q, want %q", status.InstanceName, instanceName)
	}
	if status.Machine.PrivateIP == "" || status.Machine.AppPort != sandbox.DefaultAppPort || status.Machine.LinuxUser != fixture.Tenant || !status.Machine.Running {
		t.Fatalf("status machine = %#v", status.Machine)
	}
	if !status.Machine.ContainerTools {
		t.Fatalf("status machine ContainerTools = false, want true")
	}
	if sshKey := fixture.Config.SSHPublicKey; sshKey != "" {
		authKeys := strings.TrimSpace(execInstanceOutput(t, projectServer, instanceName, []string{"cat", "/home/" + fixture.Tenant + "/.ssh/authorized_keys"}))
		if !strings.Contains(authKeys, sshKey) {
			t.Fatalf("SSH public key not found in /home/%s/.ssh/authorized_keys", fixture.Tenant)
		}
	}
}

func TestCLIAddDefaultEnterE2E(t *testing.T) {
	fixture := setupCLIAddProjectE2E(t, "create")
	sandcastleBin := strings.TrimSpace(fixture.Config.SandcastleBin)
	if sandcastleBin == "" {
		sandcastleBin = buildSandcastleForE2E(t)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, sandcastleBin,
		"create", fixture.SandboxRef,
		"--template", "base",
		"--home-dir", "interactive-home",
		"--workspace-dir", ".",
	)
	command.Stdin = strings.NewReader("printf 'sandcastle-add-interactive-ok\\n'; whoami; pwd; exit\n")
	command.Env = append(os.Environ(), sandcastleCLIEnv(fixture)...)
	output, err := command.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("sandcastle create default enter timed out\n%s", strings.TrimSpace(string(output)))
	}
	if err != nil {
		t.Fatalf("sandcastle create default enter: %v\n%s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "sandcastle-add-interactive-ok") {
		t.Fatalf("sandcastle create default enter output missing marker:\n%s", strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), fixture.Tenant) {
		t.Fatalf("sandcastle create default enter output missing Linux user %q:\n%s", fixture.Tenant, strings.TrimSpace(string(output)))
	}

	projectServer := fixture.Server.UseProject(fixture.Project.IncusProject)
	instanceName := "default-" + fixture.SandboxName
	assertInstanceExists(t, projectServer, instanceName)
	instance, _, err := projectServer.GetInstance(instanceName)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Devices["home"]["source"] != project.HomeVolumeName+"/interactive-home" {
		t.Fatalf("home source = %q", instance.Devices["home"]["source"])
	}
	if instance.Devices["home"]["path"] != "/home/"+fixture.Tenant {
		t.Fatalf("home path = %q", instance.Devices["home"]["path"])
	}
	assertSandboxIngressFiles(t, projectServer, instanceName, fixture.SandboxName+".default."+fixture.Project.DNSSuffix, sandbox.DefaultAppPort)
}

type cliAddFixture struct {
	Config      Config
	Server      incus.InstanceServer
	Project     project.CreatePlan
	Tenant      string
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
	tenant := safeProjectName("tenant-" + runID)
	name := safeProjectName("project-" + runID)
	sandboxName := safeProjectName(suffix + "-" + runID)
	ref := tenant
	sandboxRef := sandboxName
	baseAlias := "sandcastle/base:" + safeToken(runID) + "-" + suffix
	aiAlias := "sandcastle/ai:" + safeToken(runID) + "-" + suffix
	adminConfig := config.Admin{
		Tenant:                ref,
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
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable project %s", ref)
			return
		}
		if err := projectDeleter.DeleteTenant(ctx, deletePlan); err != nil {
			t.Logf("cleanup failed for %s: %v", ref, err)
		}
	})

	existing, err := project.List(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	createProjectPlan, err := project.PlanCreate(adminConfig, project.CreateRequest{
		Reference:     ref,
		SSHPublicKey:  e2eConfig.SSHPublicKey,
		OccupiedCIDRs: project.OccupiedCIDRs(existing),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateTenant(ctx, createProjectPlan); err != nil {
		t.Fatal(err)
	}
	namespacePlan, err := project.PlanCreateProject(ctx, adminConfig, store, project.ProjectMutationRequest{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	if err := incusx.NewProjectSSHKeyManager(e2eConfig.Remote).SetTenantProjects(ctx, namespacePlan.IncusProject, namespacePlan.Projects); err != nil {
		t.Fatal(err)
	}

	return cliAddFixture{
		Config:      e2eConfig,
		Server:      server,
		Project:     createProjectPlan,
		Tenant:      tenant,
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
		"SANDCASTLE_TENANT=" + fixture.Tenant,
		"SANDCASTLE_STORAGE_POOL=" + fixture.Config.StoragePool,
		"SANDCASTLE_CIDR_POOL=" + fixture.Config.CIDRPool,
		"SANDCASTLE_PROJECT_PREFIX=" + config.DefaultProjectPrefix,
		"SANDCASTLE_INFRA_PROJECT=" + config.DefaultInfrastructureProject,
		"SANDCASTLE_BASE_IMAGE=" + fixture.BaseAlias,
		"SANDCASTLE_AI_IMAGE=" + fixture.AIAlias,
	}
}
