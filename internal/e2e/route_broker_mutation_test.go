package e2e

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	sharedtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/infra"
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/route"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
	"github.com/thieso2/sandcastle-incus/internal/usertrust"
)

func TestRouteBrokerAuthorizedMutationE2E(t *testing.T) {
	e2eConfig := LoadConfig()
	if !e2eConfig.Enabled {
		t.Skip("set SANDCASTLE_E2E=1 to run destructive real Incus e2e tests")
	}
	if err := e2eConfig.Validate(); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(e2eConfig.RouteBroker.IncusSocket) == "" {
		t.Skip("set SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET to run broker mutation e2e")
	}
	baseSource := strings.TrimSpace(e2eConfig.Images.BaseSource)
	aiSource := strings.TrimSpace(e2eConfig.Images.AISource)
	if baseSource == "" || aiSource == "" {
		t.Skip("set SANDCASTLE_E2E_BASE_IMAGE_SOURCE and SANDCASTLE_E2E_AI_IMAGE_SOURCE to already-imported Sandcastle image aliases")
	}

	sandcastleBin := strings.TrimSpace(e2eConfig.SandcastleBin)
	if sandcastleBin == "" {
		sandcastleBin = buildSandcastleForE2E(t)
	}
	t.Setenv("SANDCASTLE_BIN", sandcastleBin)

	ctx := context.Background()
	runID := e2eConfig.DisposableRunID()
	owner := safeProjectName("owner-" + runID)
	otherOwner := safeProjectName("other-" + runID)
	name := safeProjectName("broker-" + runID)
	_ = name
	otherName := safeProjectName("other-broker-" + runID)
	sandboxName := safeProjectName("box-" + runID)
	otherSandboxName := safeProjectName("other-box-" + runID)
	ref := owner
	otherRef := otherOwner + "/" + otherName
	sandboxRef := sandboxName
	otherSandboxRef := otherRef + "/" + otherSandboxName
	publicDomain := strings.Trim(strings.TrimSpace(e2eConfig.PublicRoutes.Domain), ".")
	if publicDomain == "" {
		publicDomain = "example.com"
	}
	hostname := "route-" + safeToken(runID) + "." + publicDomain
	unownedHostname := "unowned-route-" + safeToken(runID) + "." + publicDomain
	dnsFailHostname := "dns-fail-route-" + safeToken(runID) + "." + publicDomain
	infrastructureHost := strings.TrimSpace(e2eConfig.PublicRoutes.InfrastructureHost)
	if infrastructureHost == "" {
		infrastructureHost = "127.0.0.1"
	}
	infraProject := safeInfrastructureProject("sc-infra-" + runID)
	baseAlias := "sandcastle/base:" + safeToken(runID) + "-broker"
	aiAlias := "sandcastle/ai:" + safeToken(runID) + "-broker"
	adminConfig := config.Admin{
		Tenant:                 ref,
		Remote:                 e2eConfig.Remote,
		StoragePool:            e2eConfig.StoragePool,
		CIDRPool:               e2eConfig.CIDRPool,
		ProjectPrefix:          config.DefaultProjectPrefix,
		InfrastructureProject:  infraProject,
		InfrastructureHost:     infrastructureHost,
		LetsEncryptEmail:       strings.TrimSpace(e2eConfig.PublicRoutes.LetsEncryptEmail),
		RouteBrokerIncusSocket: strings.TrimSpace(e2eConfig.RouteBroker.IncusSocket),
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

	infraCreator := incusx.NewInfrastructureCreator(e2eConfig.Remote)
	infraDeleter := incusx.NewInfrastructureDeleter(e2eConfig.Remote)
	infraDeletePlan, err := infra.PlanDelete(adminConfig, infra.DeleteRequest{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable infrastructure project %s", infraProject)
			return
		}
		if err := infraDeleter.DeleteInfrastructure(ctx, infraDeletePlan); err != nil {
			t.Logf("cleanup failed for infrastructure project %s: %v", infraProject, err)
		}
	})
	infraCreatePlan, err := infra.PlanCreate(adminConfig, infra.CreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := infraCreator.CreateInfrastructure(ctx, infraCreatePlan); err != nil {
		t.Fatal(err)
	}
	infraServer := server.UseProject(infraProject)
	registerInfrastructureCaddyDiagnostics(t, infraServer)

	imageManager := incusx.NewImageManager(e2eConfig.Remote)
	syncImageAlias(t, ctx, imageManager, adminConfig, baseSource)
	syncImageAlias(t, ctx, imageManager, adminConfig, aiSource)

	store := incusx.NewProjectStore(e2eConfig.Remote)
	registerProjectDiagnostics(t, ctx, store, incusx.NewTopologyStore(e2eConfig.Remote), runID)
	projectCreator := incusx.NewProjectCreator(e2eConfig.Remote)
	projectDeleter := incusx.NewProjectDeleter(e2eConfig.Remote)
	projectDeletePlan, err := project.PlanDelete(adminConfig, project.DeleteRequest{Reference: ref, Purge: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable project %s", ref)
			return
		}
		if err := projectDeleter.DeleteTenant(ctx, projectDeletePlan); err != nil {
			t.Logf("cleanup failed for %s: %v", ref, err)
		}
	})
	existing, err := project.List(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	createProjectPlan, err := project.PlanCreate(adminConfig, project.CreateRequest{
		Reference:     ref,
		OccupiedCIDRs: project.OccupiedCIDRs(existing),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := projectCreator.CreateTenant(ctx, createProjectPlan); err != nil {
		t.Fatal(err)
	}
	existing, err = project.List(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	createOtherProjectPlan, err := project.PlanCreate(adminConfig, project.CreateRequest{
		Reference:     otherRef,
		OccupiedCIDRs: project.OccupiedCIDRs(existing),
	})
	if err != nil {
		t.Fatal(err)
	}
	otherProjectDeletePlan, err := project.PlanDelete(adminConfig, project.DeleteRequest{Reference: otherRef, Purge: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable project %s", otherRef)
			return
		}
		if err := projectDeleter.DeleteTenant(ctx, otherProjectDeletePlan); err != nil {
			t.Logf("cleanup failed for %s: %v", otherRef, err)
		}
	})
	if err := projectCreator.CreateTenant(ctx, createOtherProjectPlan); err != nil {
		t.Fatal(err)
	}

	sandboxPlan, err := sandbox.PlanCreate(ctx, adminConfig, store, incusx.NewHostOverrideManager(e2eConfig.Remote), sandbox.CreateRequest{
		Reference: sandboxRef,
		Template:  "base",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := incusx.NewSandboxCreator(e2eConfig.Remote).CreateMachine(ctx, sandboxPlan); err != nil {
		t.Fatal(err)
	}
	otherSandboxPlan, err := sandbox.PlanCreate(ctx, adminConfig, store, incusx.NewHostOverrideManager(e2eConfig.Remote), sandbox.CreateRequest{
		Reference: otherSandboxRef,
		Template:  "base",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := incusx.NewSandboxCreator(e2eConfig.Remote).CreateMachine(ctx, otherSandboxPlan); err != nil {
		t.Fatal(err)
	}
	targetServer := server.UseProject(createProjectPlan.IncusProject)
	publicBody := "sandcastle-public-route"
	startSandboxHTTPApp(t, targetServer, sandboxPlan.InstanceName, sandboxPlan.AppPort, publicBody)

	certPEM, keyPEM := createRouteBrokerE2ECertificate(t, e2eConfig, server, owner)
	certPath, keyPath := writeRouteBrokerClientFiles(t, infraServer, string(certPEM), string(keyPEM))
	addRouteBrokerHostsEntry(t, infraServer, hostname, adminConfig.InfrastructureHost)
	addRouteBrokerHostsEntry(t, infraServer, dnsFailHostname, wrongInfrastructureHost(adminConfig.InfrastructureHost))

	output := execInstanceOutput(t, infraServer, infra.RouteBrokerName, []string{
		"python3", "-c", routeBrokerAddProbeScript(certPath, keyPath, hostname, sandboxRef, unownedHostname, otherSandboxRef, dnsFailHostname),
	})
	for _, want := range []string{"UNOWNED 403", "DNS-PROOF 400", "ADD 201", "LIST-ADD 200"} {
		if !strings.Contains(output, want) {
			t.Fatalf("broker mutation output missing %q:\n%s", want, output)
		}
	}
	assertInfrastructureRoutePort(t, infraServer, hostname, sandboxPlan.PrivateIP, sandboxPlan.AppPort)
	if publicRouteExternalCheckEnabled(e2eConfig) {
		waitForPublicHTTPSRoute(t, hostname, adminConfig.InfrastructureHost, publicBody)
	}
	portPlan, err := sandbox.PlanSetPort(ctx, adminConfig, store, sandbox.PortSetRequest{
		Reference: sandboxRef,
		AppPort:   5174,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := incusx.NewSandboxPortSetter(e2eConfig.Remote).SetAppPort(ctx, portPlan); err != nil {
		t.Fatal(err)
	}
	assertInfrastructureRoutePort(t, infraServer, hostname, sandboxPlan.PrivateIP, sandboxPlan.AppPort)
	caddyfile := readInstanceFile(t, infraServer, route.InfrastructureCaddyName, "/etc/caddy/Caddyfile")
	if strings.Contains(caddyfile, sandboxPlan.PrivateIP+":5174") {
		t.Fatalf("infrastructure route was not pinned after sandbox app port change: %q", caddyfile)
	}
	output = execInstanceOutput(t, infraServer, infra.RouteBrokerName, []string{
		"python3", "-c", routeBrokerRemoveProbeScript(certPath, keyPath, hostname),
	})
	for _, want := range []string{"REMOVE 200", "LIST-REMOVE 200"} {
		if !strings.Contains(output, want) {
			t.Fatalf("broker mutation output missing %q:\n%s", want, output)
		}
	}
	assertInfrastructureRouteAbsent(t, infraServer, hostname)
	target, _, err := targetServer.GetInstance(sandboxPlan.InstanceName)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := target.Devices[route.ProfileName(hostname)]; ok {
		t.Fatalf("route ingress device was not removed from %s: %#v", sandboxPlan.InstanceName, target.Devices)
	}
	if _, _, err := infraServer.GetProfile(route.ProfileName(hostname)); !api.StatusErrorCheck(err, 404) {
		t.Fatalf("expected route profile cleanup for %s, err = %v", hostname, err)
	}
	otherTargetServer := server.UseProject(createOtherProjectPlan.IncusProject)
	otherTarget, _, err := otherTargetServer.GetInstance(otherSandboxPlan.InstanceName)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := otherTarget.Devices[route.ProfileName(unownedHostname)]; ok {
		t.Fatalf("unowned route ingress device was attached to %s: %#v", otherSandboxPlan.InstanceName, otherTarget.Devices)
	}
	if _, _, err := infraServer.GetProfile(route.ProfileName(unownedHostname)); !api.StatusErrorCheck(err, 404) {
		t.Fatalf("expected no route profile for rejected unowned route %s, err = %v", unownedHostname, err)
	}
	assertInfrastructureRouteAbsent(t, infraServer, unownedHostname)
	if _, _, err := infraServer.GetProfile(route.ProfileName(dnsFailHostname)); !api.StatusErrorCheck(err, 404) {
		t.Fatalf("expected no route profile for rejected DNS proof route %s, err = %v", dnsFailHostname, err)
	}
	assertInfrastructureRouteAbsent(t, infraServer, dnsFailHostname)
}

func publicRouteExternalCheckEnabled(config Config) bool {
	return strings.TrimSpace(config.PublicRoutes.Domain) != "" &&
		strings.TrimSpace(config.PublicRoutes.InfrastructureHost) != "" &&
		strings.TrimSpace(config.PublicRoutes.LetsEncryptEmail) != ""
}

func registerInfrastructureCaddyDiagnostics(t *testing.T, server incus.InstanceServer) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		logInfrastructureCaddyDiagnostic(t, server)
	})
}

func logInfrastructureCaddyDiagnostic(t *testing.T, server incus.InstanceServer) {
	t.Helper()
	reader, response, err := server.GetInstanceFile(route.InfrastructureCaddyName, "/etc/caddy/Caddyfile")
	if err != nil {
		t.Logf("diagnostics: infrastructure Caddyfile read failed: %v", err)
		return
	}
	defer reader.Close()
	if response.Type != "file" {
		t.Logf("diagnostics: infrastructure Caddyfile type=%q", response.Type)
		return
	}
	data, err := io.ReadAll(io.LimitReader(reader, 16*1024))
	if err != nil {
		t.Logf("diagnostics: infrastructure Caddyfile content read failed: %v", err)
		return
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		t.Log("diagnostics: infrastructure Caddyfile empty")
		return
	}
	t.Logf("diagnostics: infrastructure Caddyfile:\n%s", indentDiagnosticContent(redactDiagnosticValue(content), "  "))
}

func waitForPublicHTTPSRoute(t *testing.T, hostname string, infrastructureHost string, want string) {
	t.Helper()
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				ServerName: hostname,
			},
			DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
				if strings.EqualFold(addr, net.JoinHostPort(hostname, "443")) {
					addr = hostPort(infrastructureHost, "443")
				}
				return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, addr)
			},
		},
	}
	deadline := time.Now().Add(3 * time.Minute)
	var last string
	for time.Now().Before(deadline) {
		response, err := client.Get("https://" + hostname + "/")
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK && strings.Contains(string(body), want) {
				return
			}
			if readErr != nil {
				last = readErr.Error()
			} else {
				last = fmt.Sprintf("status = %s body = %q", response.Status, string(body))
			}
		} else {
			last = err.Error()
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("trusted HTTPS request to public route %s through %s did not return %q: %s", hostname, infrastructureHost, want, last)
}

func hostPort(host string, defaultPort string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, defaultPort)
}

func wrongInfrastructureHost(target string) string {
	if strings.TrimSpace(target) == "198.51.100.254" {
		return "198.51.100.253"
	}
	return "198.51.100.254"
}

func assertInfrastructureRoutePort(t *testing.T, server incus.InstanceServer, hostname string, targetIP string, routePort int) {
	t.Helper()
	caddyfile := readInstanceFile(t, server, route.InfrastructureCaddyName, "/etc/caddy/Caddyfile")
	expected := "reverse_proxy http://" + targetIP + ":" + strconv.Itoa(routePort)
	if !strings.Contains(caddyfile, hostname) || !strings.Contains(caddyfile, expected) {
		t.Fatalf("infrastructure Caddyfile missing pinned route %s/%s: %q", hostname, expected, caddyfile)
	}
}

func assertInfrastructureRouteAbsent(t *testing.T, server incus.InstanceServer, hostname string) {
	t.Helper()
	caddyfile := readInstanceFile(t, server, route.InfrastructureCaddyName, "/etc/caddy/Caddyfile")
	if strings.Contains(caddyfile, hostname) {
		t.Fatalf("infrastructure Caddyfile still contains route %s: %q", hostname, caddyfile)
	}
}

func createRouteBrokerE2ECertificate(t *testing.T, e2eConfig Config, server incus.InstanceServer, owner string) ([]byte, []byte) {
	t.Helper()
	certPEM, keyPEM, err := sharedtls.GenerateMemCert(true, false)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := sharedtls.CertFingerprintStr(string(certPEM))
	if err != nil {
		t.Fatal(err)
	}
	certificateName := usertrust.CertificateNamePrefix + owner
	if err := server.CreateCertificate(api.CertificatesPost{CertificatePut: api.CertificatePut{
		Name:        certificateName,
		Type:        api.CertificateTypeClient,
		Restricted:  true,
		Projects:    []string{},
		Certificate: string(certPEM),
		Description: "Sandcastle route broker mutation e2e user " + owner,
	}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e2eConfig.Keep {
			t.Logf("keeping disposable certificate %s", certificateName)
			return
		}
		if err := server.DeleteCertificate(fingerprint); err != nil {
			t.Logf("cleanup failed for certificate %s: %v", certificateName, err)
		}
	})
	return certPEM, keyPEM
}

func addRouteBrokerHostsEntry(t *testing.T, server incus.InstanceServer, hostname string, target string) {
	t.Helper()
	_ = execInstanceOutput(t, server, infra.RouteBrokerName, []string{
		"/bin/sh", "-lc", "printf '%s %s\n' " + shellQuote(target) + " " + shellQuote(hostname) + " >> /etc/hosts",
	})
}

func routeBrokerAddProbeScript(certPath string, keyPath string, hostname string, targetRef string, unownedHostname string, unownedTargetRef string, dnsFailHostname string) string {
	return `
import json, ssl, sys, time, urllib.error, urllib.request
cert_path = ` + pythonQuote(certPath) + `
key_path = ` + pythonQuote(keyPath) + `
hostname = ` + pythonQuote(hostname) + `
target_ref = ` + pythonQuote(targetRef) + `
unowned_hostname = ` + pythonQuote(unownedHostname) + `
unowned_target_ref = ` + pythonQuote(unownedTargetRef) + `
dns_fail_hostname = ` + pythonQuote(dnsFailHostname) + `
context = ssl.create_default_context()
context.check_hostname = False
context.verify_mode = ssl.CERT_NONE
context.load_cert_chain(cert_path, key_path)

def request(method, path, payload=None):
    data = None
    headers = {}
    if payload is not None:
        data = json.dumps(payload).encode('utf-8')
        headers['Content-Type'] = 'application/json'
    req = urllib.request.Request('https://127.0.0.1:9443' + path, data=data, method=method, headers=headers)
    return urllib.request.urlopen(req, context=context, timeout=3)

last = ''
for _ in range(50):
    try:
        response = request('GET', '/routes')
        response.read()
        break
    except Exception as err:
        last = repr(err)
        time.sleep(0.2)
else:
    print('READY-ERROR', last)
    sys.exit(1)

try:
    try:
        response = request('POST', '/routes', {'hostname': unowned_hostname, 'targetReference': unowned_target_ref})
        print('UNOWNED-UNEXPECTED', response.status, response.read().decode('utf-8'))
        sys.exit(1)
    except urllib.error.HTTPError as err:
        body = err.read().decode('utf-8')
        print('UNOWNED', err.code)
        print('UNOWNED-BODY', body)
        if err.code != 403:
            sys.exit(1)
    try:
        response = request('POST', '/routes', {'hostname': dns_fail_hostname, 'targetReference': target_ref})
        print('DNS-PROOF-UNEXPECTED', response.status, response.read().decode('utf-8'))
        sys.exit(1)
    except urllib.error.HTTPError as err:
        body = err.read().decode('utf-8')
        print('DNS-PROOF', err.code)
        print('DNS-PROOF-BODY', body)
        if err.code != 400:
            sys.exit(1)
    response = request('POST', '/routes', {'hostname': hostname, 'targetReference': target_ref})
    print('ADD', response.status)
    print('ADD-BODY', response.read().decode('utf-8'))
    response = request('GET', '/routes')
    list_body = response.read().decode('utf-8')
    print('LIST-ADD', response.status)
    print('LIST-ADD-BODY', list_body)
    if hostname not in list_body:
        sys.exit(1)
except urllib.error.HTTPError as err:
    print('HTTP-ERROR', err.code, err.read().decode('utf-8'))
    sys.exit(1)
except Exception as err:
    print('ERROR', repr(err))
    sys.exit(1)
`
}

func routeBrokerRemoveProbeScript(certPath string, keyPath string, hostname string) string {
	return `
import json, ssl, sys, time, urllib.error, urllib.request
cert_path = ` + pythonQuote(certPath) + `
key_path = ` + pythonQuote(keyPath) + `
hostname = ` + pythonQuote(hostname) + `
context = ssl.create_default_context()
context.check_hostname = False
context.verify_mode = ssl.CERT_NONE
context.load_cert_chain(cert_path, key_path)

def request(method, path, payload=None):
    data = None
    headers = {}
    if payload is not None:
        data = json.dumps(payload).encode('utf-8')
        headers['Content-Type'] = 'application/json'
    req = urllib.request.Request('https://127.0.0.1:9443' + path, data=data, method=method, headers=headers)
    return urllib.request.urlopen(req, context=context, timeout=3)

try:
    response = request('DELETE', '/routes/' + hostname)
    print('REMOVE', response.status)
    print('REMOVE-BODY', response.read().decode('utf-8'))
    response = request('GET', '/routes')
    list_body = response.read().decode('utf-8')
    print('LIST-REMOVE', response.status)
    print('LIST-REMOVE-BODY', list_body)
    if hostname in list_body:
        sys.exit(1)
except urllib.error.HTTPError as err:
    print('HTTP-ERROR', err.code, err.read().decode('utf-8'))
    sys.exit(1)
except Exception as err:
    print('ERROR', repr(err))
    sys.exit(1)
`
}
