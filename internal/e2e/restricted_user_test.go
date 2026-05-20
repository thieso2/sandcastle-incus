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
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/project"
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

	user := safeProjectName("user-" + e2eConfig.DisposableRunID())
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
	user := safeProjectName("owner-" + runID)
	ownedName := safeProjectName("owned-" + runID)
	deniedOwner := safeProjectName("other-" + runID)
	deniedName := safeProjectName("denied-" + runID)
	ownedRef := user + "/" + ownedName
	deniedRef := deniedOwner + "/" + deniedName
	adminConfig := config.Admin{
		Remote:                e2eConfig.Remote,
		StoragePool:           e2eConfig.StoragePool,
		CIDRPool:              e2eConfig.CIDRPool,
		ProjectPrefix:         config.DefaultProjectPrefix,
		InfrastructureProject: config.DefaultInfrastructureProject,
		Images: config.Images{
			Base: config.DefaultBaseImageAlias,
			AI:   config.DefaultAIImageAlias,
		},
	}
	ownedProject := createRestrictedAccessProject(t, e2eConfig, adminConfig, ownedRef, ownedName+"."+e2eConfig.DomainSuffix, "10.248.220.0/24")
	deniedProject := createRestrictedAccessProject(t, e2eConfig, adminConfig, deniedRef, deniedName+"."+e2eConfig.DomainSuffix, "10.248.221.0/24")

	adminServer, err := e2eInstanceServer(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}
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
	ownedProjectServer := restricted.UseProject(ownedProject)
	if _, _, err := ownedProjectServer.GetProject(ownedProject); err != nil {
		t.Fatalf("restricted user cannot access owned project %s: %v", ownedProject, err)
	}
	summaries, err := project.List(ctx, incusx.ProjectStore{Server: restricted})
	if err != nil {
		t.Fatalf("restricted user cannot list project metadata: %v", err)
	}
	if !hasProjectSummary(summaries, user, ownedName) {
		t.Fatalf("restricted project list missing owned project %s: %#v", ownedRef, summaries)
	}
	if hasProjectSummary(summaries, deniedOwner, deniedName) {
		t.Fatalf("restricted project list included denied project %s: %#v", deniedRef, summaries)
	}
	if _, _, err := restricted.UseProject(deniedProject).GetProject(deniedProject); err == nil {
		t.Fatalf("restricted user unexpectedly accessed denied project %s", deniedProject)
	}
	globalProjectName := safeProjectName("global-" + runID)
	if err := restricted.CreateProject(api.ProjectsPost{Name: globalProjectName}); err == nil {
		_ = adminServer.DeleteProject(globalProjectName)
		t.Fatalf("restricted user unexpectedly created global project %s", globalProjectName)
	}
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

func createRestrictedAccessProject(t *testing.T, e2eConfig Config, adminConfig config.Admin, ref string, domain string, privateCIDR string) string {
	t.Helper()
	projectRef, err := naming.ParseProjectRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	incusName, err := naming.IncusProjectNameWithPrefix(adminConfig.ProjectPrefix, projectRef)
	if err != nil {
		t.Fatal(err)
	}
	configMap, err := meta.ProjectConfig(meta.Project{
		Owner:           projectRef.Owner,
		Project:         projectRef.Project,
		Domain:          domain,
		PrivateCIDR:     privateCIDR,
		DefaultTemplate: "ai",
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
			t.Logf("keeping disposable project %s", incusName)
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

func hasProjectSummary(summaries []project.Summary, owner string, name string) bool {
	for _, summary := range summaries {
		if summary.Owner == owner && summary.Name == name {
			return true
		}
	}
	return false
}
