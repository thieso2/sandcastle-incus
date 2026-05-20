package routebroker

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	sharedtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/thieso2/sandcastle-incus/internal/certs"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

func TestPlanServeDefaultsAddress(t *testing.T) {
	plan, err := PlanServe(ServeRequest{
		CertFile: "/tmp/broker.crt",
		KeyFile:  "/tmp/broker.key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Address != ":9443" {
		t.Fatalf("Address = %q", plan.Address)
	}
}

func TestPlanServeRequiresTLSCertificate(t *testing.T) {
	_, err := PlanServe(ServeRequest{KeyFile: "/tmp/broker.key"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("error = %q", err)
	}
}

func TestPlanServeRequiresTLSKey(t *testing.T) {
	_, err := PlanServe(ServeRequest{CertFile: "/tmp/broker.crt"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "key") {
		t.Fatalf("error = %q", err)
	}
}

func TestHTTPRunnerServesAuthorizedRouteOverMTLS(t *testing.T) {
	serverTLS, err := certs.GenerateSelfSignedServer("route broker", []string{"localhost"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile := writeTLSFiles(t, serverTLS)
	clientCertPEM, clientKeyPEM, err := sharedtls.GenerateMemCert(true, false)
	if err != nil {
		t.Fatal(err)
	}
	clientCertFile, clientKeyFile := writePEMFiles(t, clientCertPEM, clientKeyPEM)

	routes := &fakeBrokerRoutes{list: route.ListResult{Routes: []route.Route{{
		Hostname:        "app.example.com",
		TargetReference: "alice/myproject/codex",
		RoutePort:       5173,
	}}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	address := freeLocalAddress(t)
	done := make(chan error, 1)
	go func() {
		done <- (HTTPRunner{Server: brokerServerForTest(t, routes, fakeBrokerMetadata{})}).Serve(ctx, ServePlan{
			Address:  address,
			CertFile: certFile,
			KeyFile:  keyFile,
		})
	}()
	client := Client{
		BaseURL:            "https://" + address,
		CertFile:           clientCertFile,
		KeyFile:            clientKeyFile,
		InsecureSkipVerify: true,
	}

	addRouteWithRetry(t, client)
	if routes.added == nil {
		t.Fatal("expected route add")
	}
	result, err := client.List(ctx, route.ListPlan{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 1 || result.Routes[0].Hostname != "app.example.com" {
		t.Fatalf("routes = %#v", result.Routes)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func writeTLSFiles(t *testing.T, pair certs.KeyPair) (string, string) {
	t.Helper()
	dir := t.TempDir()
	certFile := dir + "/tls.crt"
	keyFile := dir + "/tls.key"
	if err := os.WriteFile(certFile, pair.CertificatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pair.PrivateKeyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func writePEMFiles(t *testing.T, certPEM []byte, keyPEM []byte) (string, string) {
	t.Helper()
	dir := t.TempDir()
	certFile := dir + "/client.crt"
	keyFile := dir + "/client.key"
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func freeLocalAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

func addRouteWithRetry(t *testing.T, client Client) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		err := client.Add(context.Background(), route.AddPlan{
			Hostname:        "app.example.com",
			TargetReference: "alice/myproject/codex",
		})
		if err == nil {
			return
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("post route over mTLS: %v", lastErr)
}
