package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/images"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/infra"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

const infrastructureKind = "infrastructure"

func TestCleanupDisposableResourcesE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e cleanup")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}
	runToken, err := cleanupRunToken(e2eConfig)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}

	adminConfig := config.Admin{
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
	ctx := context.Background()
	store := incusx.NewTenantStore(e2eConfig.Remote)
	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	server, err := e2eInstanceServer(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}

	tenantDeleter := incusx.NewTenantDeleter(e2eConfig.Remote)
	infraDeleter := incusx.NewInfrastructureDeleter(e2eConfig.Remote)
	deletedProjects := 0
	deletedInfrastructure := 0
	for _, incusProject := range projects {
		switch incusProject.Config[meta.KeyKind] {
		case meta.KindTenant:
			if !managedProjectMatchesRun(incusProject, runToken) {
				continue
			}
			t.Logf("cleanup matched project %s", incusProject.Name)
			managed, err := meta.ParseTenantConfig(incusProject.Config)
			if err != nil {
				t.Fatalf("parse project metadata for cleanup target %s: %v", incusProject.Name, err)
			}
			deletePlan, err := tenant.PlanDelete(adminConfig, tenant.DeleteRequest{
				Reference: managed.Tenant,
				Purge:     true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := tenantDeleter.DeleteTenant(ctx, deletePlan); err != nil {
				t.Fatalf("cleanup project %s: %v", deletePlan.Reference, err)
			}
			deletedProjects++
		case infrastructureKind:
			if !managedInfrastructureMatchesRun(incusProject, runToken) {
				continue
			}
			t.Logf("cleanup matched infrastructure project %s", incusProject.Name)
			deletePlan, err := infra.PlanDelete(adminConfig, infra.DeleteRequest{Project: incusProject.Name})
			if err != nil {
				t.Fatal(err)
			}
			if err := infraDeleter.DeleteInfrastructure(ctx, deletePlan); err != nil {
				t.Fatalf("cleanup infrastructure project %s: %v", incusProject.Name, err)
			}
			deletedInfrastructure++
		}
	}
	deletedCertificates, err := cleanupDisposableCertificates(t, server, runToken)
	if err != nil {
		t.Fatal(err)
	}
	deletedImageAliases, err := cleanupDisposableImageAliases(t, server, runToken)
	if err != nil {
		t.Fatal(err)
	}
	deletedLocalImages := cleanupDisposableLocalImageTags(t, e2eConfig, runToken)
	t.Logf("cleanup run %q removed %d project(s), %d infrastructure project(s), %d certificate(s), %d image alias(es), and %d local image tag(s)", runToken, deletedProjects, deletedInfrastructure, deletedCertificates, deletedImageAliases, deletedLocalImages)
}

func cleanupRunToken(config Config) (string, error) {
	runToken := safeToken(strings.TrimSpace(config.RunID))
	if runToken == "" {
		return "", fmt.Errorf("SANDCASTLE_E2E_RUN_ID is required for standalone e2e cleanup")
	}
	if len(runToken) < 8 {
		return "", fmt.Errorf("SANDCASTLE_E2E_RUN_ID %q is too short for standalone e2e cleanup", config.RunID)
	}
	return runToken, nil
}

func managedProjectMatchesRun(incusProject tenant.IncusProject, runToken string) bool {
	if strings.Contains(incusProject.Name, runToken) {
		return true
	}
	managed, err := meta.ParseTenantConfig(incusProject.Config)
	if err != nil {
		return false
	}
	for _, value := range []string{managed.Tenant, managed.PrivateCIDR} {
		if strings.Contains(value, runToken) {
			return true
		}
	}
	return false
}

func managedInfrastructureMatchesRun(incusProject tenant.IncusProject, runToken string) bool {
	if strings.Contains(incusProject.Name, runToken) {
		return true
	}
	return strings.Contains(incusProject.Config[meta.Prefix+"name"], runToken)
}

type cleanupResourceServer interface {
	GetCertificates() ([]api.Certificate, error)
	DeleteCertificate(fingerprint string) error
	GetImageAliases() ([]api.ImageAliasesEntry, error)
	DeleteImageAlias(name string) error
}

func cleanupDisposableCertificates(t *testing.T, server cleanupResourceServer, runToken string) (int, error) {
	t.Helper()
	certificates, err := server.GetCertificates()
	if err != nil {
		return 0, fmt.Errorf("list Incus certificates for cleanup: %w", err)
	}
	deleted := 0
	for _, certificate := range certificates {
		if !managedCertificateMatchesRun(certificate, runToken) {
			continue
		}
		t.Logf("cleanup matched restricted certificate %s", certificate.Name)
		if err := server.DeleteCertificate(certificate.Fingerprint); err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			return deleted, fmt.Errorf("delete certificate %s: %w", certificate.Name, err)
		}
		deleted++
	}
	return deleted, nil
}

func cleanupDisposableImageAliases(t *testing.T, server cleanupResourceServer, runToken string) (int, error) {
	t.Helper()
	aliases, err := server.GetImageAliases()
	if err != nil {
		return 0, fmt.Errorf("list Incus image aliases for cleanup: %w", err)
	}
	deleted := 0
	for _, alias := range aliases {
		if !managedImageAliasMatchesRun(alias, runToken) {
			continue
		}
		t.Logf("cleanup matched image alias %s", alias.Name)
		if err := server.DeleteImageAlias(alias.Name); err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			return deleted, fmt.Errorf("delete image alias %s: %w", alias.Name, err)
		}
		deleted++
	}
	return deleted, nil
}

func cleanupDisposableLocalImageTags(t *testing.T, e2eConfig Config, runToken string) int {
	t.Helper()
	tool := strings.TrimSpace(e2eConfig.Images.BuildTool)
	if tool == "" {
		tool = "docker"
	}
	if _, err := exec.LookPath(tool); err != nil {
		t.Logf("cleanup skipped local image tags: %s not found", tool)
		return 0
	}
	deleted := 0
	runner := images.ExecRunner{}
	for _, tag := range disposableLocalImageTags(runToken) {
		if err := runner.Run(context.Background(), tool, "image", "inspect", tag); err != nil {
			continue
		}
		t.Logf("cleanup matched local image tag %s", tag)
		if err := runner.Run(context.Background(), tool, "image", "rm", tag); err != nil {
			t.Logf("cleanup failed for local image tag %s: %v", tag, err)
			continue
		}
		deleted++
	}
	return deleted
}

func disposableLocalImageTags(runToken string) []string {
	return []string{
		"sandcastle/base:" + runToken,
		"sandcastle/base:" + runToken + "-ai-base",
		"sandcastle/ai:" + runToken,
	}
}

func managedCertificateMatchesRun(certificate api.Certificate, runToken string) bool {
	if !strings.HasPrefix(certificate.Name, usertrust.CertificateNamePrefix) && !strings.Contains(certificate.Description, "Sandcastle") {
		return false
	}
	return stringFieldsContainRun(runToken, append([]string{certificate.Name, certificate.Description}, certificate.Projects...))
}

func managedImageAliasMatchesRun(alias api.ImageAliasesEntry, runToken string) bool {
	if !strings.HasPrefix(alias.Name, "sandcastle/base:") && !strings.HasPrefix(alias.Name, "sandcastle/ai:") {
		return false
	}
	return stringFieldsContainRun(runToken, []string{alias.Name, alias.Description})
}

func stringFieldsContainRun(runToken string, fields []string) bool {
	for _, field := range fields {
		if strings.Contains(field, runToken) {
			return true
		}
	}
	return false
}

func TestCleanupRunTokenRequiresExplicitLongRunID(t *testing.T) {
	if _, err := cleanupRunToken(Config{}); err == nil {
		t.Fatal("expected missing run id error")
	}
	if _, err := cleanupRunToken(Config{RunID: "short"}); err == nil {
		t.Fatal("expected short run id error")
	}
	token, err := cleanupRunToken(Config{RunID: "e2e-20260520-120000"})
	if err != nil {
		t.Fatal(err)
	}
	if token != "e2e-20260520-120000" {
		t.Fatalf("token = %q", token)
	}
}

func TestCleanupProjectSelectionMatchesOnlyRunID(t *testing.T) {
	config, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "tenant-e2e-20260520-120000",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !managedProjectMatchesRun(tenant.IncusProject{Name: "sc-tenant-e2e-20260520-120000", Config: config}, "e2e-20260520-120000") {
		t.Fatal("expected project cleanup match")
	}
	if managedProjectMatchesRun(tenant.IncusProject{Name: "sc-tenant-other", Config: config}, "e2e-19990101-000000") {
		t.Fatal("unexpected project cleanup match")
	}
}

func TestCleanupInfrastructureSelectionMatchesOnlyRunID(t *testing.T) {
	project := tenant.IncusProject{
		Name: "sc-infra-e2e-20260520-120000",
		Config: map[string]string{
			meta.KeyKind:         infrastructureKind,
			meta.KeyVersion:      "1",
			meta.Prefix + "name": "sc-infra-e2e-20260520-120000",
		},
	}
	if !managedInfrastructureMatchesRun(project, "e2e-20260520-120000") {
		t.Fatal("expected infrastructure cleanup match")
	}
	if managedInfrastructureMatchesRun(project, "e2e-19990101-000000") {
		t.Fatal("unexpected infrastructure cleanup match")
	}
}

func TestCleanupCertificateSelectionMatchesOnlySandcastleRunID(t *testing.T) {
	certificate := api.Certificate{
		Fingerprint: "abcd",
		CertificatePut: api.CertificatePut{
			Name:        "sandcastle-user-e2e-20260520-120000",
			Type:        api.CertificateTypeClient,
			Restricted:  true,
			Description: "Sandcastle restricted user user-e2e-20260520-120000",
			Projects:    []string{"sc-tenant-e2e-20260520-120000"},
		},
	}
	if !managedCertificateMatchesRun(certificate, "e2e-20260520-120000") {
		t.Fatal("expected certificate cleanup match")
	}
	if managedCertificateMatchesRun(certificate, "e2e-19990101-000000") {
		t.Fatal("unexpected certificate cleanup match")
	}
	certificate.Name = "admin-e2e-20260520-120000"
	certificate.Description = "manual admin cert e2e-20260520-120000"
	if managedCertificateMatchesRun(certificate, "e2e-20260520-120000") {
		t.Fatal("unexpected unmanaged certificate cleanup match")
	}
}

func TestCleanupImageAliasSelectionMatchesOnlySandcastleRunID(t *testing.T) {
	alias := api.ImageAliasesEntry{
		Name: "sandcastle/base:e2e-20260520-120000",
		ImageAliasesEntryPut: api.ImageAliasesEntryPut{
			Description: "Sandcastle e2e base alias e2e-20260520-120000",
			Target:      "abc123",
		},
	}
	if !managedImageAliasMatchesRun(alias, "e2e-20260520-120000") {
		t.Fatal("expected image alias cleanup match")
	}
	if managedImageAliasMatchesRun(alias, "e2e-19990101-000000") {
		t.Fatal("unexpected image alias cleanup match")
	}
	alias.Name = "ubuntu:e2e-20260520-120000"
	if managedImageAliasMatchesRun(alias, "e2e-20260520-120000") {
		t.Fatal("unexpected unmanaged image alias cleanup match")
	}
}

func TestCleanupLocalImageTagsUseRunID(t *testing.T) {
	tags := disposableLocalImageTags("e2e-20260520-120000")
	want := []string{
		"sandcastle/base:e2e-20260520-120000",
		"sandcastle/base:e2e-20260520-120000-ai-base",
		"sandcastle/ai:e2e-20260520-120000",
	}
	if strings.Join(tags, "\n") != strings.Join(want, "\n") {
		t.Fatalf("tags = %#v, want %#v", tags, want)
	}
	for _, tag := range tags {
		if !strings.Contains(tag, "e2e-20260520-120000") {
			t.Fatalf("tag %q missing run id", tag)
		}
	}
}
