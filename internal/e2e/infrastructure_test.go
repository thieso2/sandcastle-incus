package e2e

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/cliconfig"
	sharedtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/infra"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

func TestDisposableInfrastructureCreateAndDelete(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}
	sandcastleBin := strings.TrimSpace(e2eConfig.SandcastleBin)
	if sandcastleBin == "" {
		sandcastleBin = buildSandcastleForE2E(t)
	}
	t.Setenv("SANDCASTLE_BIN", sandcastleBin)

	ctx := context.Background()
	runID := e2eConfig.DisposableRunID()
	infraProject := safeInfrastructureProject("sc-infra-" + runID)
	adminConfig := config.Admin{
		Remote:                e2eConfig.Remote,
		StoragePool:           e2eConfig.StoragePool,
		CIDRPool:              e2eConfig.CIDRPool,
		ProjectPrefix:         config.DefaultProjectPrefix,
		InfrastructureProject: infraProject,
		Images: config.Images{
			Base: config.DefaultBaseImageAlias,
			AI:   config.DefaultAIImageAlias,
		},
	}
	creator := incusx.NewInfrastructureCreator(e2eConfig.Remote)
	deleter := incusx.NewInfrastructureDeleter(e2eConfig.Remote)
	deletePlan, err := infra.PlanDelete(adminConfig, infra.DeleteRequest{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable infrastructure project %s", infraProject)
			return
		}
		if err := deleter.DeleteInfrastructure(ctx, deletePlan); err != nil {
			t.Logf("cleanup failed for infrastructure project %s: %v", infraProject, err)
		}
	})

	createPlan, err := infra.PlanCreate(adminConfig, infra.CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := creator.CreateInfrastructure(ctx, createPlan); err != nil {
		t.Fatal(err)
	}

	server, err := e2eInstanceServer(e2eConfig.Remote)
	if err != nil {
		t.Fatal(err)
	}
	projectServer := server.UseProject(infraProject)
	assertInstanceExists(t, projectServer, route.InfrastructureCaddyName)
	assertInstanceExists(t, projectServer, infra.RouteBrokerName)
	assertRouteBrokerMTLS(t, projectServer)

	if err := deleter.DeleteInfrastructure(ctx, deletePlan); err != nil {
		t.Fatal(err)
	}
}

func assertRouteBrokerMTLS(t *testing.T, server incus.InstanceServer) {
	t.Helper()
	certPEM, keyPEM, err := sharedtls.GenerateMemCert(true, false)
	if err != nil {
		t.Fatal(err)
	}
	clientCertPath := "/tmp/sandcastle-route-broker-client.crt"
	clientKeyPath := "/tmp/sandcastle-route-broker-client.key"
	for _, file := range []struct {
		path    string
		content string
	}{
		{path: clientCertPath, content: string(certPEM)},
		{path: clientKeyPath, content: string(keyPEM)},
	} {
		if err := server.CreateInstanceFile(infra.RouteBrokerName, file.path, incus.InstanceFileArgs{
			Content:   strings.NewReader(file.content),
			Type:      "file",
			Mode:      0o600,
			WriteMode: "overwrite",
		}); err != nil {
			t.Fatalf("write route broker client TLS file %s: %v", file.path, err)
		}
	}
	output := execInstanceOutput(t, server, infra.RouteBrokerName, []string{
		"python3", "-c", routeBrokerMTLSProbeScript(clientCertPath, clientKeyPath),
	})
	if !strings.Contains(output, "STATUS 404") {
		t.Fatalf("route broker mTLS probe output = %q, want STATUS 404", output)
	}
}

func routeBrokerMTLSProbeScript(certPath string, keyPath string) string {
	return `
import ssl, sys, time, urllib.error, urllib.request
cert_path = ` + pythonQuote(certPath) + `
key_path = ` + pythonQuote(keyPath) + `
last = ''
for _ in range(50):
    try:
        context = ssl.create_default_context()
        context.check_hostname = False
        context.verify_mode = ssl.CERT_NONE
        context.load_cert_chain(cert_path, key_path)
        urllib.request.urlopen('https://127.0.0.1:9443/routes', context=context, timeout=1)
        print('STATUS 200')
        sys.exit(1)
    except urllib.error.HTTPError as err:
        print('STATUS', err.code)
        sys.exit(0 if err.code == 404 else 1)
    except Exception as err:
        last = repr(err)
        time.sleep(0.2)
print('ERROR', last)
sys.exit(1)
`
}

func buildSandcastleForE2E(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sandcastle")
	command := exec.Command("go", "build", "-o", path, "./cmd/sandcastle")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build sandcastle e2e binary: %v\n%s", err, strings.TrimSpace(string(output)))
	}
	return path
}

func e2eInstanceServer(remote string) (incus.InstanceServer, error) {
	loaded, err := cliconfig.LoadConfig("")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(remote) == "" {
		remote = loaded.DefaultRemote
	}
	return loaded.GetInstanceServer(remote)
}

func assertInstanceExists(t *testing.T, server incus.InstanceServer, name string) {
	t.Helper()
	if _, _, err := server.GetInstance(name); err != nil {
		t.Fatalf("expected instance %s: %v", name, err)
	}
}

func safeInfrastructureProject(value string) string {
	value = safeToken(value)
	if len(value) > 50 {
		value = value[:50]
	}
	return strings.Trim(value, "-")
}
