package e2e

import (
	"context"
	"os"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	sharedtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

func TestRestrictedUserTokenE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}

	user := safeTenantResourceName("user-" + e2eConfig.DisposableRunID())
	plan, err := usertrust.PlanToken(user)
	if err != nil {
		t.Fatal(err)
	}
	result, err := incusx.NewTrustManager(e2eConfig.Remote).CreateToken(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Token == "" {
		t.Fatal("expected certificate add token")
	}
	decoded, err := sharedtls.CertificateTokenDecode(result.Token)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ClientName != plan.CertificateName {
		t.Fatalf("token client name = %q, want %q", decoded.ClientName, plan.CertificateName)
	}
	if decoded.Secret == "" || decoded.Fingerprint == "" || len(decoded.Addresses) == 0 {
		t.Fatalf("decoded token is incomplete: %#v", decoded)
	}
}

func TestRestrictedUserGrantAccessE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	loaded, remoteName, remote, err := e2eRestrictedRemote(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(remote.Addr, "unix:") {
		t.Skip("restricted certificate e2e requires an HTTPS Incus remote, not a local Unix socket remote")
	}

	runID := e2eConfig.DisposableRunID()
	user := safeTenantResourceName("user-" + runID)
	ownedName := safeTenantResourceName("owned-" + runID)
	deniedName := safeTenantResourceName("denied-" + runID)
	ownedRef := ownedName
	deniedRef := deniedName
	adminConfig := config.Admin{
		Tenant:                ownedRef,
		Remote:                e2eConfig.Remote,
		StoragePool:           e2eConfig.StoragePool,
		CIDRPool:              e2eConfig.CIDRPool,
		IncusProjectPrefix:    config.DefaultIncusProjectPrefix,
		InfrastructureProject: config.DefaultInfrastructureProject,
		Images: config.Images{
			Base: config.DefaultBaseImageAlias,
			AI:   config.DefaultAIImageAlias,
		},
	}
	ownedProject := createRestrictedAccessProject(t, e2eConfig, adminConfig, ownedRef, "10.248.220.0/24")
	deniedProject := createRestrictedAccessProject(t, e2eConfig, adminConfig, deniedRef, "10.248.221.0/24")

	adminServer, err := e2eInstanceServer(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM := createRestrictedClientCertificate(t, e2eConfig, adminServer, user)

	grantPlan, err := usertrust.PlanGrant(adminConfig, usertrust.GrantRequest{User: user, Projects: []string{ownedRef}})
	if err != nil {
		t.Fatal(err)
	}
	if err := incusx.NewTrustManager(e2eConfig.Remote).Grant(ctx, grantPlan); err != nil {
		t.Fatal(err)
	}

	restricted, err := restrictedInstanceServer(loaded, remoteName, remote, certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	ownedTenantListServer := restricted.UseProject(ownedProject)
	if _, _, err := ownedTenantListServer.GetProject(ownedProject); err != nil {
		t.Fatalf("restricted user cannot access owned project %s: %v", ownedProject, err)
	}
	summaries, err := tenant.List(ctx, incusx.TenantStore{Server: restricted})
	if err != nil {
		t.Fatalf("restricted user cannot list project metadata: %v", err)
	}
	if !hasProjectSummary(summaries, user, ownedName) {
		t.Fatalf("restricted project list missing owned project %s: %#v", ownedRef, summaries)
	}
	if hasProjectSummary(summaries, "", deniedName) {
		t.Fatalf("restricted project list included denied project %s: %#v", deniedRef, summaries)
	}
	if _, _, err := restricted.UseProject(deniedProject).GetProject(deniedProject); err == nil {
		t.Fatalf("restricted user unexpectedly accessed denied project %s", deniedProject)
	}
	globalProjectName := safeTenantResourceName("global-" + runID)
	if err := restricted.CreateProject(api.ProjectsPost{Name: globalProjectName}); err == nil {
		_ = adminServer.DeleteProject(globalProjectName)
		t.Fatalf("restricted user unexpectedly created global project %s", globalProjectName)
	}
}

func TestRestrictedUserMachineLifecycleE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run real Incus e2e tests")
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
	loaded, remoteName, remote, err := e2eRestrictedRemote(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(remote.Addr, "unix:") {
		t.Skip("restricted machine lifecycle e2e requires an HTTPS Incus remote, not a local Unix socket remote")
	}

	runID := e2eConfig.DisposableRunID()
	user := safeTenantResourceName("user-" + runID)
	name := safeTenantResourceName("restricted-" + runID)
	machineName := safeTenantResourceName("box-" + runID)
	ref := name
	machineRef := machineName
	baseAlias := "sandcastle/base:" + safeToken(runID) + "-restricted"
	aiAlias := "sandcastle/ai:" + safeToken(runID) + "-restricted"
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

	adminServer, err := e2eInstanceServer(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupImageAlias(t, e2eConfig, adminServer, aiAlias))
	t.Cleanup(cleanupImageAlias(t, e2eConfig, adminServer, baseAlias))

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

	certPEM, keyPEM := createRestrictedClientCertificate(t, e2eConfig, adminServer, user)
	grantPlan, err := usertrust.PlanGrant(adminConfig, usertrust.GrantRequest{User: user, Projects: []string{ref}})
	if err != nil {
		t.Fatal(err)
	}
	if err := incusx.NewTrustManager(e2eConfig.Remote).Grant(ctx, grantPlan); err != nil {
		t.Fatal(err)
	}
	restricted, err := restrictedInstanceServer(loaded, remoteName, remote, certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}

	restrictedStore := incusx.TenantStore{Server: restricted}
	restrictedMachines := incusx.HostOverrideManager{Server: e2eHostOverrideServer{inner: restricted}}
	createMachinePlan, err := machine.PlanCreate(ctx, adminConfig, restrictedStore, restrictedMachines, machine.CreateRequest{Reference: machineRef})
	if err != nil {
		t.Fatal(err)
	}
	if err := (incusx.MachineCreator{Server: e2eMachineCreateServer{inner: restricted}}).CreateMachine(ctx, createMachinePlan); err != nil {
		t.Fatal(err)
	}

	projectServer := restricted.UseProject(createTenantPlan.IncusProject)
	assertInstanceExists(t, projectServer, createMachinePlan.InstanceName)
	hostname := machineName + ".default." + createTenantPlan.DNSSuffix
	assertMachineIngressFiles(t, projectServer, createMachinePlan.InstanceName, hostname, createMachinePlan.AppPort)
	startMachineHTTPApp(t, projectServer, createMachinePlan.InstanceName, createMachinePlan.AppPort, "sandcastle-restricted")
	assertMachineCaddyProxy(t, projectServer, createMachinePlan.InstanceName, hostname, "sandcastle-restricted")

	controller := incusx.MachineController{Server: e2eMachineLifecycleServer{inner: restricted}}
	for _, action := range []machine.Action{machine.ActionStop, machine.ActionStart, machine.ActionDelete} {
		plan, err := machine.PlanLifecycle(ctx, adminConfig, restrictedStore, restrictedMachines, machine.LifecycleRequest{
			Reference: machineRef,
			Action:    action,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := controller.ApplyLifecycle(ctx, plan); err != nil {
			t.Fatalf("%s machine as restricted user: %v", action, err)
		}
	}
	if _, _, err := projectServer.GetInstance(createMachinePlan.InstanceName); !api.StatusErrorCheck(err, 404) {
		t.Fatalf("expected restricted machine %s to be deleted, err = %v", createMachinePlan.InstanceName, err)
	}
}

func createRestrictedClientCertificate(t *testing.T, e2eConfig Config, adminServer incus.InstanceServer, user string) ([]byte, []byte) {
	t.Helper()
	certPEM, keyPEM, err := sharedtls.GenerateMemCert(true, false)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := sharedtls.CertFingerprintStr(string(certPEM))
	if err != nil {
		t.Fatal(err)
	}
	certificateName := usertrust.CertificateNamePrefix + user
	if err := adminServer.CreateCertificate(api.CertificatesPost{CertificatePut: api.CertificatePut{
		Name:        certificateName,
		Type:        api.CertificateTypeClient,
		Restricted:  true,
		Projects:    []string{},
		Certificate: string(certPEM),
		Description: "Sandcastle restricted user " + user,
	}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable certificate %s", certificateName)
			return
		}
		if err := adminServer.DeleteCertificate(fingerprint); err != nil {
			t.Logf("cleanup failed for certificate %s: %v", certificateName, err)
		}
	})
	return certPEM, keyPEM
}

type e2eHostOverrideServer struct {
	inner incus.InstanceServer
}

func (s e2eHostOverrideServer) UseProject(name string) incusx.HostOverrideResourceServer {
	return e2eIncusResource{InstanceServer: s.inner.UseProject(name)}
}

type e2eMachineCreateServer struct {
	inner incus.InstanceServer
}

func (s e2eMachineCreateServer) UseProject(name string) incusx.MachineResourceServer {
	return e2eIncusResource{InstanceServer: s.inner.UseProject(name)}
}

type e2eMachineLifecycleServer struct {
	inner incus.InstanceServer
}

func (s e2eMachineLifecycleServer) UseProject(name string) incusx.MachineLifecycleResourceServer {
	return e2eIncusResource{InstanceServer: s.inner.UseProject(name)}
}

type e2eIncusResource struct {
	incus.InstanceServer
}

func e2eRestrictedRemote(remoteName string) (*cliconfig.Config, string, cliconfig.Remote, error) {
	loaded, err := cliconfig.LoadConfig("")
	if err != nil {
		return nil, "", cliconfig.Remote{}, err
	}
	if strings.TrimSpace(remoteName) == "" {
		remoteName = loaded.DefaultRemote
	}
	remote, ok := loaded.Remotes[remoteName]
	if !ok {
		return nil, "", cliconfig.Remote{}, os.ErrNotExist
	}
	return loaded, remoteName, remote, nil
}

func restrictedInstanceServer(loaded *cliconfig.Config, remoteName string, remote cliconfig.Remote, certPEM []byte, keyPEM []byte) (incus.InstanceServer, error) {
	args := incus.ConnectionArgs{
		TLSClientCert: string(certPEM),
		TLSClientKey:  string(keyPEM),
	}
	if content, err := os.ReadFile(loaded.ServerCertPath(remoteName)); err == nil {
		args.TLSServerCert = string(content)
	}
	return incus.ConnectIncus(remote.Addr, &args)
}

func createRestrictedAccessProject(t *testing.T, e2eConfig Config, adminConfig config.Admin, ref string, privateCIDR string) string {
	t.Helper()
	tenantRef, err := naming.ParseTenantRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	incusName, err := naming.TenantIncusProjectNameWithPrefix(adminConfig.IncusProjectPrefix, tenantRef)
	if err != nil {
		t.Fatal(err)
	}
	configMap, err := meta.TenantConfig(meta.Tenant{
		Tenant:      tenantRef.Tenant,
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: privateCIDR,
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := e2eInstanceServer(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable tenant %s", incusName)
			return
		}
		if err := server.DeleteProject(incusName); err != nil && !api.StatusErrorCheck(err, 404) {
			t.Logf("cleanup failed for project %s: %v", incusName, err)
		}
	})
	if err := server.CreateProject(api.ProjectsPost{
		Name: incusName,
		ProjectPut: api.ProjectPut{
			Description: "Sandcastle restricted access e2e project " + ref,
			Config:      api.ConfigMap(configMap),
		},
	}); err != nil {
		t.Fatal(err)
	}
	return incusName
}

func hasProjectSummary(summaries []tenant.Summary, fallbackTenant string, name string) bool {
	tenant := name
	if tenant == "" {
		tenant = fallbackTenant
	}
	for _, summary := range summaries {
		if summary.Tenant == tenant {
			return true
		}
	}
	return false
}
