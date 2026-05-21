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
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
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
	tenantName := safeTenantResourceName("tenant-" + runID)
	machineName := safeTenantResourceName("box-" + runID)
	ref := tenantName
	machineRef := machineName
	baseAlias := "sandcastle/base:" + safeToken(runID) + "-host"
	aiAlias := "sandcastle/ai:" + safeToken(runID) + "-host"
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
		OccupiedCIDRs: tenant.OccupiedCIDRs(existing),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateTenant(ctx, createTenantPlan); err != nil {
		t.Fatal(err)
	}

	machineStore := incusx.NewHostOverrideManager(e2eConfig.Remote)
	createMachinePlan, err := machine.PlanCreate(ctx, adminConfig, store, machineStore, machine.CreateRequest{Reference: machineRef})
	if err != nil {
		t.Fatal(err)
	}
	if err := incusx.NewMachineCreator(e2eConfig.Remote).CreateMachine(ctx, createMachinePlan); err != nil {
		t.Fatal(err)
	}
	projectServer := server.UseProject(createTenantPlan.IncusProject)
	overrideHost := "app-" + safeToken(runID) + ".override.test"
	hostname := machineName + ".default." + createTenantPlan.DNSSuffix
	startMachineHTTPApp(t, projectServer, createMachinePlan.InstanceName, createMachinePlan.AppPort, "sandcastle-host-override")
	assertMachineCaddyProxy(t, projectServer, createMachinePlan.InstanceName, hostname, "sandcastle-host-override")

	hostsPath := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hostsPath, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hostsManager := hostoverride.FileHostsManager{Path: hostsPath}
	overrideManager := incusx.NewHostOverrideManager(e2eConfig.Remote)
	addPlan, err := hostoverride.PlanAdd(ctx, adminConfig, store, machineStore, hostoverride.AddRequest{
		Reference: machineRef,
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
	assertMachineCaddyProxy(t, projectServer, createMachinePlan.InstanceName, overrideHost, "sandcastle-host-override")
	assertCertificateForHost(t, readInstanceFile(t, projectServer, createMachinePlan.InstanceName, machine.MachineCertPath), overrideHost)

	listResult, err := hostoverride.PlanList(ctx, adminConfig, store, machineStore, hostoverride.ListRequest{Reference: ref})
	if err != nil {
		t.Fatal(err)
	}
	if !containsHostOverride(listResult, overrideHost) {
		t.Fatalf("host override list missing %s: %#v", overrideHost, listResult.Overrides)
	}

	overrideDeletePlan, err := hostoverride.PlanDelete(ctx, adminConfig, store, machineStore, hostoverride.DeleteRequest{
		Reference: machineRef,
		Hostname:  overrideHost,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := overrideManager.Delete(ctx, overrideDeletePlan); err != nil {
		t.Fatal(err)
	}
	if err := hostsManager.RemoveHostsEntry(ctx, overrideDeletePlan); err != nil {
		t.Fatal(err)
	}
	assertHostsFileMissing(t, hostsPath, overrideHost)
	caddyfile := readInstanceFile(t, projectServer, createMachinePlan.InstanceName, machine.CaddyfilePath)
	if strings.Contains(caddyfile, overrideHost) {
		t.Fatalf("machine Caddyfile still contains deleted override %q: %q", overrideHost, caddyfile)
	}
	assertCertificateNotForHost(t, readInstanceFile(t, projectServer, createMachinePlan.InstanceName, machine.MachineCertPath), overrideHost)
	assertMachineCaddyProxy(t, projectServer, createMachinePlan.InstanceName, hostname, "sandcastle-host-override")
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
	reference := "tenant-" + safeToken(runID) + "/default/box-" + safeToken(runID)
	hostname := "host-" + safeToken(runID) + ".override.test"
	entry := hostoverride.RenderHostsEntry(reference, hostname, "127.0.0.1")
	manager := hostoverride.NewFileHostsManager("")
	ctx := context.Background()

	deletePlan := hostoverride.DeletePlan{HostsEntry: entry}
	_ = manager.RemoveHostsEntry(ctx, deletePlan)
	added := false
	t.Cleanup(func() {
		if !added {
			return
		}
		if err := manager.RemoveHostsEntry(context.Background(), deletePlan); err != nil {
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

	if err := manager.RemoveHostsEntry(ctx, deletePlan); err != nil {
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
		t.Fatalf("machine certificate is not a CERTIFICATE PEM block")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse machine certificate: %v", err)
	}
	if err := certificate.VerifyHostname(hostname); err == nil {
		t.Fatalf("machine certificate still verifies deleted hostname %q", hostname)
	}
}
