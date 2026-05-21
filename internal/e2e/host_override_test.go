package e2e

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/hostoverride"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestHostOverrideE2E(t *testing.T) {
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
	baseAlias := "sandcastle/base:" + safeToken(runID) + "-host"
	aiAlias := "sandcastle/ai:" + safeToken(runID) + "-host"
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

	sandboxStore := incusx.NewHostOverrideManager(e2eConfig.Remote)
	createSandboxPlan, err := sandbox.PlanCreate(ctx, adminConfig, store, sandboxStore, sandbox.CreateRequest{Reference: sandboxRef})
	if err != nil {
		t.Fatal(err)
	}
	if err := incusx.NewSandboxCreator(e2eConfig.Remote).CreateSandbox(ctx, createSandboxPlan); err != nil {
		t.Fatal(err)
	}
	projectServer := server.UseProject(createProjectPlan.IncusProject)
	overrideHost := "app-" + safeToken(runID) + ".override.test"
	hostname := sandboxName + "." + createProjectPlan.Domain
	startSandboxHTTPApp(t, projectServer, createSandboxPlan.InstanceName, createSandboxPlan.AppPort, "sandcastle-host-override")
	assertSandboxCaddyProxy(t, projectServer, createSandboxPlan.InstanceName, hostname, "sandcastle-host-override")

	hostsPath := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hostsPath, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hostsManager := hostoverride.FileHostsManager{Path: hostsPath}
	overrideManager := incusx.NewHostOverrideManager(e2eConfig.Remote)
	addPlan, err := hostoverride.PlanAdd(ctx, adminConfig, store, sandboxStore, hostoverride.AddRequest{
		Reference: sandboxRef,
		Hostname:  overrideHost,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := overrideManager.Add(ctx, addPlan); err != nil {
		t.Fatal(err)
	}
	if err := hostsManager.AddHostsEntry(ctx, addPlan); err != nil {
		t.Fatal(err)
	}
	assertHostsFileContains(t, hostsPath, overrideHost)
	assertSandboxCaddyProxy(t, projectServer, createSandboxPlan.InstanceName, overrideHost, "sandcastle-host-override")
	assertCertificateForHost(t, readInstanceFile(t, projectServer, createSandboxPlan.InstanceName, sandbox.SandboxCertPath), overrideHost)

	listResult, err := hostoverride.PlanList(ctx, adminConfig, store, sandboxStore, hostoverride.ListRequest{Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	if !containsHostOverride(listResult, overrideHost) {
		t.Fatalf("host override list missing %s: %#v", overrideHost, listResult.Overrides)
	}

	removePlan, err := hostoverride.PlanRemove(ctx, adminConfig, store, sandboxStore, hostoverride.RemoveRequest{
		Reference: sandboxRef,
		Hostname:  overrideHost,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := overrideManager.Remove(ctx, removePlan); err != nil {
		t.Fatal(err)
	}
	if err := hostsManager.RemoveHostsEntry(ctx, removePlan); err != nil {
		t.Fatal(err)
	}
	assertHostsFileMissing(t, hostsPath, overrideHost)
	caddyfile := readInstanceFile(t, projectServer, createSandboxPlan.InstanceName, sandbox.CaddyfilePath)
	if strings.Contains(caddyfile, overrideHost) {
		t.Fatalf("sandbox Caddyfile still contains removed override %q: %q", overrideHost, caddyfile)
	}
	assertCertificateNotForHost(t, readInstanceFile(t, projectServer, createSandboxPlan.InstanceName, sandbox.SandboxCertPath), overrideHost)
	assertSandboxCaddyProxy(t, projectServer, createSandboxPlan.InstanceName, hostname, "sandcastle-host-override")
}

func TestHostOverrideHostsFileE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}
	if !e2eConfig.LocalVM {
		t.Skip("set SANDCASTLE_E2E_LOCAL_VM=1 to run disposable-VM /etc/hosts e2e tests")
	}

	runID := e2eConfig.DisposableRunID()
	reference := "owner-" + safeToken(runID) + "/project-" + safeToken(runID) + "/box-" + safeToken(runID)
	hostname := "host-" + safeToken(runID) + ".override.test"
	entry := hostoverride.RenderHostsEntry(reference, hostname, "127.0.0.1")
	manager := hostoverride.NewFileHostsManager("")
	ctx := context.Background()

	removePlan := hostoverride.RemovePlan{HostsEntry: entry}
	_ = manager.RemoveHostsEntry(ctx, removePlan)
	added := false
	t.Cleanup(func() {
		if !added {
			return
		}
		if err := manager.RemoveHostsEntry(context.Background(), removePlan); err != nil {
			t.Logf("hosts cleanup failed: %v", err)
		}
	})

	if err := manager.AddHostsEntry(ctx, hostoverride.AddPlan{HostsEntry: entry}); err != nil {
		t.Fatal(err)
	}
	added = true
	assertHostsFileContains(t, hostoverride.DefaultHostsPath, hostname)
	assertHostsFileContains(t, hostoverride.DefaultHostsPath, entry.BeginLine)
	assertHostsFileContains(t, hostoverride.DefaultHostsPath, entry.EndLine)

	if err := manager.RemoveHostsEntry(ctx, removePlan); err != nil {
		t.Fatal(err)
	}
	added = false
	assertHostsFileMissing(t, hostoverride.DefaultHostsPath, hostname)
	assertHostsFileMissing(t, hostoverride.DefaultHostsPath, entry.BeginLine)
	assertHostsFileMissing(t, hostoverride.DefaultHostsPath, entry.EndLine)
}

func assertHostsFileContains(t *testing.T, path string, hostname string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), hostname) {
		t.Fatalf("hosts file = %q, want %q", content, hostname)
	}
}

func assertHostsFileMissing(t *testing.T, path string, hostname string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), hostname) {
		t.Fatalf("hosts file = %q, want no %q", content, hostname)
	}
}

func containsHostOverride(result hostoverride.ListResult, hostname string) bool {
	for _, override := range result.Overrides {
		if override.Hostname == hostname {
			return true
		}
	}
	return false
}

func assertCertificateNotForHost(t *testing.T, certPEM string, hostname string) {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("sandbox certificate is not a CERTIFICATE PEM block")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse sandbox certificate: %v", err)
	}
	if err := certificate.VerifyHostname(hostname); err == nil {
		t.Fatalf("sandbox certificate still verifies removed hostname %q", hostname)
	}
}
