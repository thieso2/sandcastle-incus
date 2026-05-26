package e2e

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

// TestCLILoginE2E exercises the full sc login --debug-approve flow against the live
// auth app. It provisions (or re-provisions) the personal tenant for the debug user,
// enrolls a restricted Incus remote, and verifies all three Incus projects and both
// sidecar instances are present and running.
//
// Prerequisites:
//   - SANDCASTLE_E2E_AUTH_HOST set to the auth app URL (e.g. https://big.thieso2.dev)
//   - Auth app running with --debug-device-user set to an allowlisted user
//   - SANDCASTLE_E2E_DEBUG_USER set to that user (default: "thieso2")
//   - SANDCASTLE_E2E_REMOTE / SANDCASTLE_E2E_STORAGE_POOL / SANDCASTLE_E2E_CIDR_POOL set
func TestCLILoginE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e tests")
	}
	authHost := strings.TrimSpace(e2eConfig.Auth.Host)
	if authHost == "" {
		t.Skip("set SANDCASTLE_E2E_AUTH_HOST to the auth app URL to run login e2e")
	}
	debugUser := strings.TrimSpace(e2eConfig.Auth.DebugUser)
	if debugUser == "" {
		t.Fatal("SANDCASTLE_E2E_DEBUG_USER must be set")
	}

	sandcastleBin := strings.TrimSpace(e2eConfig.SandcastleBin)
	if sandcastleBin == "" {
		sandcastleBin = buildSandcastleForE2E(t)
	}

	ctx := context.Background()
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
	incusProject, err := naming.PersonalTenantIncusProjectNameWithPrefix(adminConfig.IncusProjectPrefix, debugUser)
	if err != nil {
		t.Fatalf("derive incus project name: %v", err)
	}
	deletePlan, err := tenant.PlanDelete(adminConfig, tenant.DeleteRequest{Reference: debugUser, Purge: true})
	if err != nil {
		t.Fatal(err)
	}
	deleter := incusx.NewTenantDeleter(e2eConfig.Remote)

	// Delete the personal tenant so we exercise fresh provisioning.
	t.Logf("purging personal tenant %s before login test", debugUser)
	if err := deleter.DeleteTenant(ctx, deletePlan); err != nil {
		t.Logf("pre-cleanup: %v", err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping personal tenant %s after login test", debugUser)
			return
		}
		if err := deleter.DeleteTenant(ctx, deletePlan); err != nil {
			t.Logf("post-cleanup failed: %v", err)
		}
	})

	// Run sc login in an isolated home directory so config state doesn't bleed into
	// the developer's real Sandcastle config.
	homeDir := t.TempDir()
	cmdCtx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	loginCmd := exec.CommandContext(cmdCtx, sandcastleBin,
		"login",
		"--debug-approve",
		"--skip-setup",
		authHost,
	)
	loginCmd.Env = loginCLIEnv(e2eConfig, homeDir)
	loginCmd.Stdout = os.Stdout
	loginCmd.Stderr = os.Stderr
	t.Logf("running: %s login --debug-approve --skip-setup %s", filepath.Base(sandcastleBin), authHost)
	if err := loginCmd.Run(); err != nil {
		t.Fatalf("sc login: %v", err)
	}

	// Verify all three Incus projects were created.
	server, err := e2eInstanceServer(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}
	infraProject := naming.TenantInfraIncusProjectName(incusProject)
	nativeProject := naming.TenantNativeIncusProjectName(incusProject)
	for _, proj := range []string{incusProject, infraProject, nativeProject} {
		if _, _, err := server.GetProject(proj); err != nil {
			t.Errorf("expected Incus project %s to exist: %v", proj, err)
		}
	}

	// Verify sidecar instances are present and running in the infra project.
	infraServer := server.UseProject(infraProject)
	tsName := tenant.TailscaleInstanceName(incusProject)
	for _, name := range []string{tsName, tenant.DNSName} {
		instance, _, err := infraServer.GetInstance(name)
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			t.Errorf("sidecar %s not found in %s", name, infraProject)
			continue
		}
		if err != nil {
			t.Errorf("get sidecar %s: %v", name, err)
			continue
		}
		if !instance.IsActive() {
			t.Errorf("sidecar %s is not running (status=%s)", name, instance.Status)
		}
	}

	// Verify sc tailscale status can reach the sidecar in the infra project.
	// This catches regressions where the exec targets the wrong Incus project.
	tsStatusCtx, tsStatusCancel := context.WithTimeout(ctx, 30*time.Second)
	defer tsStatusCancel()
	tsStatusCmd := exec.CommandContext(tsStatusCtx, sandcastleBin, "tailscale", "status", "--output", "json")
	tsStatusCmd.Env = loginCLIEnv(e2eConfig, homeDir)
	if out, err := tsStatusCmd.CombinedOutput(); err != nil {
		t.Errorf("sc tailscale status: %v\n%s", err, strings.TrimSpace(string(out)))
	}
}

// loginCLIEnv builds an env for running the user-facing sc binary during login e2e.
// HOME is redirected to homeDir so Sandcastle and Incus config land in a temp dir.
// INCUS_CONF must NOT be set so that sc login can compute the per-remote path itself.
func loginCLIEnv(e2eConfig Config, homeDir string) []string {
	filtered := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "INCUS_CONF=") || strings.HasPrefix(e, "HOME=") {
			continue
		}
		filtered = append(filtered, e)
	}
	return append(filtered,
		"HOME="+homeDir,
		"SANDCASTLE_REMOTE="+e2eConfig.Remote,
		"SANDCASTLE_STORAGE_POOL="+e2eConfig.StoragePool,
		"SANDCASTLE_CIDR_POOL="+e2eConfig.CIDRPool,
		"SANDCASTLE_INCUS_PROJECT_PREFIX="+config.DefaultIncusProjectPrefix,
		"SANDCASTLE_INFRA_PROJECT="+config.DefaultInfrastructureProject,
	)
}
