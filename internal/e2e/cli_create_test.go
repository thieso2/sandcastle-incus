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
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestCLICreateDetachE2E(t *testing.T) {
	fixture := setupCLICreateTenantE2E(t, "cli")
	setSandcastleCLIEnv(t, fixture)
	shorthandRef := fixture.ProjectName + "/" + fixture.MachineName

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
	instanceName := fixture.ProjectName + "-" + fixture.MachineName
	assertInstanceExists(t, projectServer, instanceName)
	instance, _, err := projectServer.GetInstance(instanceName)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Devices["home"]["source"] != tenant.HomeVolumeName+"/shared-home" {
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
		t.Fatalf("machine Linux user %q was not created", fixture.Tenant)
	}
	if instance.Devices["workspace"]["source"] != tenant.WorkspaceVolumeName+"/." {
		t.Fatalf("workspace source = %q", instance.Devices["workspace"]["source"])
	}
	assertMachineIngressFiles(t, projectServer, instanceName, fixture.MachineName+"."+fixture.ProjectName+"."+fixture.Project.DNSSuffix, machine.DefaultAppPort)
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
	var status machine.StatusResult
	if err := json.Unmarshal(output, &status); err != nil {
		t.Fatalf("decode status output: %v\n%s", err, strings.TrimSpace(string(output)))
	}
	if status.InstanceName != instanceName {
		t.Fatalf("status instance = %q, want %q", status.InstanceName, instanceName)
	}
	if status.Machine.PrivateIP == "" || status.Machine.AppPort != machine.DefaultAppPort || status.Machine.LinuxUser != fixture.Tenant || !status.Machine.Running {
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

func TestCLICreateDefaultConnectE2E(t *testing.T) {
	fixture := setupCLICreateTenantE2E(t, "create")
	sandcastleBin := strings.TrimSpace(fixture.Config.SandcastleBin)
	if sandcastleBin == "" {
		sandcastleBin = buildSandcastleForE2E(t)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, sandcastleBin,
		"create", fixture.MachineRef,
		"--template", "base",
		"--home-dir", "interactive-home",
		"--workspace-dir", ".",
	)
	command.Stdin = strings.NewReader("printf 'sandcastle-create-interactive-ok\\n'; whoami; pwd; exit\n")
	command.Env = append(os.Environ(), sandcastleCLIEnv(fixture)...)
	output, err := command.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("sandcastle create default connect timed out\n%s", strings.TrimSpace(string(output)))
	}
	if err != nil {
		t.Fatalf("sandcastle create default connect: %v\n%s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "sandcastle-create-interactive-ok") {
		t.Fatalf("sandcastle create default connect output missing marker:\n%s", strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), fixture.Tenant) {
		t.Fatalf("sandcastle create default connect output missing Linux user %q:\n%s", fixture.Tenant, strings.TrimSpace(string(output)))
	}

	projectServer := fixture.Server.UseProject(fixture.Project.IncusProject)
	instanceName := "default-" + fixture.MachineName
	assertInstanceExists(t, projectServer, instanceName)
	instance, _, err := projectServer.GetInstance(instanceName)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Devices["home"]["source"] != tenant.HomeVolumeName+"/interactive-home" {
		t.Fatalf("home source = %q", instance.Devices["home"]["source"])
	}
	if instance.Devices["home"]["path"] != "/home/"+fixture.Tenant {
		t.Fatalf("home path = %q", instance.Devices["home"]["path"])
	}
	assertMachineIngressFiles(t, projectServer, instanceName, fixture.MachineName+".default."+fixture.Project.DNSSuffix, machine.DefaultAppPort)
}

type cliCreateFixture struct {
	Config      Config
	Server      incus.InstanceServer
	Project     tenant.CreatePlan
	Tenant      string
	ProjectName string
	MachineRef  string
	MachineName string
	BaseAlias   string
	AIAlias     string
}

func setupCLICreateTenantE2E(t *testing.T, suffix string) cliCreateFixture {
	t.Helper()
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}
	if !e2eConfig.LocalVM {
		t.Skip("set SANDCASTLE_E2E_LOCAL_VM=1 to run tests that require direct SSH access to machine private IPs")
	}
	baseSource := strings.TrimSpace(e2eConfig.Images.BaseSource)
	aiSource := strings.TrimSpace(e2eConfig.Images.AISource)
	if baseSource == "" || aiSource == "" {
		t.Skip("set SANDCASTLE_E2E_BASE_IMAGE_SOURCE and SANDCASTLE_E2E_AI_IMAGE_SOURCE to already-imported Sandcastle image aliases")
	}

	ctx := context.Background()
	runID := e2eConfig.DisposableRunID()
	tenantName := safeTenantResourceName("tenant-" + runID)
	name := safeTenantResourceName("project-" + runID)
	machineName := safeTenantResourceName(suffix + "-" + runID)
	ref := tenantName
	machineRef := machineName
	baseAlias := "sandcastle/base:" + safeToken(runID) + "-" + suffix
	aiAlias := "sandcastle/ai:" + safeToken(runID) + "-" + suffix
	adminConfig := config.Admin{
		Tenant:                ref,
		Remote:                e2eConfig.Remote,
		StoragePool:           e2eConfig.StoragePool,
		CIDRPool:              e2eConfig.CIDRPool,
		IncusProjectPrefix:    config.DefaultIncusProjectPrefix,
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

	store := incusx.NewTenantStore(e2eConfig.Remote)
	registerTenantDiagnostics(t, ctx, store, incusx.NewTopologyStore(e2eConfig.Remote), runID)
	creator := incusx.NewTenantCreator(e2eConfig.Remote)
	tenantDeleter := incusx.NewTenantDeleter(e2eConfig.Remote)
	deletePlan, err := tenant.PlanDelete(adminConfig, tenant.DeleteRequest{Reference: ref, Purge: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable tenant %s", ref)
			return
		}
		if err := tenantDeleter.DeleteTenant(ctx, deletePlan); err != nil {
			t.Logf("cleanup failed for %s: %v", ref, err)
		}
	})

	existing, err := tenant.List(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	createTenantPlan, err := tenant.PlanCreate(adminConfig, tenant.CreateRequest{
		Reference:     ref,
		SSHPublicKey:  e2eConfig.SSHPublicKey,
		OccupiedCIDRs: tenant.OccupiedCIDRs(existing),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateTenant(ctx, createTenantPlan); err != nil {
		t.Fatal(err)
	}
	namespacePlan, err := tenant.PlanCreateProject(ctx, adminConfig, store, tenant.ProjectMutationRequest{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	if err := incusx.NewTenantSSHKeyManager(e2eConfig.Remote).SetTenantProjects(ctx, namespacePlan.IncusProject, namespacePlan.Projects); err != nil {
		t.Fatal(err)
	}

	return cliCreateFixture{
		Config:      e2eConfig,
		Server:      server,
		Project:     createTenantPlan,
		Tenant:      tenantName,
		ProjectName: name,
		MachineRef:  machineRef,
		MachineName: machineName,
		BaseAlias:   baseAlias,
		AIAlias:     aiAlias,
	}
}

func setSandcastleCLIEnv(t *testing.T, fixture cliCreateFixture) {
	t.Helper()
	for _, entry := range sandcastleCLIEnv(fixture) {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("invalid env entry %q", entry)
		}
		t.Setenv(name, value)
	}
}

func sandcastleCLIEnv(fixture cliCreateFixture) []string {
	return []string{
		"SANDCASTLE_REMOTE=" + fixture.Config.Remote,
		"SANDCASTLE_TENANT=" + fixture.Tenant,
		"SANDCASTLE_STORAGE_POOL=" + fixture.Config.StoragePool,
		"SANDCASTLE_CIDR_POOL=" + fixture.Config.CIDRPool,
		"SANDCASTLE_INCUS_PROJECT_PREFIX=" + config.DefaultIncusProjectPrefix,
		"SANDCASTLE_INFRA_PROJECT=" + config.DefaultInfrastructureProject,
		"SANDCASTLE_BASE_IMAGE=" + fixture.BaseAlias,
		"SANDCASTLE_AI_IMAGE=" + fixture.AIAlias,
		"SSH_AUTH_SOCK=",
	}
}
