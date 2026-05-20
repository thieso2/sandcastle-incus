package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	sharedtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/infra"
	"github.com/thieso2/sandcastle-incus/internal/route"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
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
		sandcastleBin = buildSandcastleForRemote(t, e2eConfig.Remote)
	}
	t.Setenv("SANDCASTLE_BIN", sandcastleBin)

	ctx := context.Background()
	runID := e2eConfig.DisposableRunID()
	infraProject := safeInfrastructureProject("sc-infra-" + runID)
	adminConfig := config.Admin{
		Remote:                 e2eConfig.Remote,
		StoragePool:            e2eConfig.StoragePool,
		CIDRPool:               e2eConfig.CIDRPool,
		ProjectPrefix:          config.DefaultProjectPrefix,
		InfrastructureProject:  infraProject,
		RouteBrokerIncusSocket: strings.TrimSpace(e2eConfig.RouteBroker.IncusSocket),
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
	if adminConfig.RouteBrokerIncusSocket != "" {
		assertRouteBrokerAuthorizedList(t, e2eConfig, server, projectServer, runID)
	}

	if err := deleter.DeleteInfrastructure(ctx, deletePlan); err != nil {
		t.Fatal(err)
	}
}

func assertRouteBrokerAuthorizedList(t *testing.T, e2eConfig Config, adminServer incus.InstanceServer, server incus.InstanceServer, runID string) {
	t.Helper()
	owner := safeProjectName("broker-" + runID)
	certPEM, keyPEM, err := sharedtls.GenerateMemCert(true, false)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := sharedtls.CertFingerprintStr(string(certPEM))
	if err != nil {
		t.Fatal(err)
	}
	certificateName := usertrust.CertificateNamePrefix + owner
	if err := adminServer.CreateCertificate(api.CertificatesPost{CertificatePut: api.CertificatePut{
		Name:        certificateName,
		Type:        api.CertificateTypeClient,
		Restricted:  true,
		Projects:    []string{},
		Certificate: string(certPEM),
		Description: "Sandcastle route broker e2e user " + owner,
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
	certPath, keyPath := writeRouteBrokerClientFiles(t, server, string(certPEM), string(keyPEM))
	output := execInstanceOutput(t, server, infra.RouteBrokerName, []string{
		"python3", "-c", routeBrokerAuthorizedListProbeScript(certPath, keyPath),
	})
	if !strings.Contains(output, "STATUS 200") {
		t.Fatalf("route broker authorized list output = %q, want STATUS 200", output)
	}
}

func writeRouteBrokerClientFiles(t *testing.T, server incus.InstanceServer, certPEM string, keyPEM string) (string, string) {
	t.Helper()
	certPath := "/tmp/sandcastle-route-broker-client.crt"
	keyPath := "/tmp/sandcastle-route-broker-client.key"
	for _, file := range []struct {
		path    string
		content string
	}{
		{path: certPath, content: certPEM},
		{path: keyPath, content: keyPEM},
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
	return certPath, keyPath
}

func assertRouteBrokerMTLS(t *testing.T, server incus.InstanceServer) {
	t.Helper()
	certPEM, keyPEM, err := sharedtls.GenerateMemCert(true, false)
	if err != nil {
		t.Fatal(err)
	}
	clientCertPath, clientKeyPath := writeRouteBrokerClientFiles(t, server, string(certPEM), string(keyPEM))
	output := execInstanceOutput(t, server, infra.RouteBrokerName, []string{
		"python3", "-c", routeBrokerMTLSProbeScript(clientCertPath, clientKeyPath),
	})
	if !strings.Contains(output, "STATUS 401") {
		t.Fatalf("route broker mTLS probe output = %q, want STATUS 401", output)
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
        sys.exit(0 if err.code == 401 else 1)
    except Exception as err:
        last = repr(err)
        time.sleep(0.2)
print('ERROR', last)
sys.exit(1)
`
}

func routeBrokerAuthorizedListProbeScript(certPath string, keyPath string) string {
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
        response = urllib.request.urlopen('https://127.0.0.1:9443/routes', context=context, timeout=1)
        body = response.read().decode('utf-8')
        print('STATUS', response.status)
        print('BODY', body)
        sys.exit(0 if response.status == 200 else 1)
    except urllib.error.HTTPError as err:
        print('STATUS', err.code)
        print('BODY', err.read().decode('utf-8'))
        sys.exit(1)
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
	command := exec.Command("go", "build", "-o", path, "github.com/thieso2/sandcastle-incus/cmd/sandcastle")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build sandcastle e2e binary: %v\n%s", err, strings.TrimSpace(string(output)))
	}
	return path
}

func buildSandcastleForRemote(t *testing.T, remote string) string {
	t.Helper()
	goarch := remoteGOARCH(t, remote)
	path := filepath.Join(t.TempDir(), "sandcastle-linux-"+goarch)
	command := exec.Command("go", "build", "-o", path, "github.com/thieso2/sandcastle-incus/cmd/sandcastle")
	command.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goarch)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build sandcastle e2e binary (linux/%s): %v\n%s", goarch, err, strings.TrimSpace(string(output)))
	}
	return path
}

func remoteGOARCH(t *testing.T, remote string) string {
	t.Helper()
	server, err := e2eInstanceServer(remote)
	if err != nil {
		t.Fatalf("connect to incus remote %q: %v", remote, err)
	}
	info, _, err := server.GetServer()
	if err != nil {
		t.Fatalf("get incus server info for %q: %v", remote, err)
	}
	switch info.Environment.KernelArchitecture {
	case "x86_64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	case "armv7l":
		return "arm"
	default:
		return "amd64"
	}
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
