package certs

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"
)

func TestGenerateCAAndIssueMachineLeaf(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	ca, err := GenerateCA("Sandcastle acme tenant CA", now)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := IssueMachineLeaf(
		ca.CertificatePEM,
		ca.PrivateKeyPEM,
		"codex.acme.sandcastle.internal",
		MachineDNSNames("codex", "acme.sandcastle.internal", []string{"app.example.test"}),
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	cert := parseLeafForTest(t, leaf.CertificatePEM)
	if len(cert.DNSNames) != 3 {
		t.Fatalf("DNSNames = %#v", cert.DNSNames)
	}
	if cert.DNSNames[0] != "codex.acme.sandcastle.internal" {
		t.Fatalf("first DNSName = %q", cert.DNSNames[0])
	}
	if cert.DNSNames[1] != "*.codex.acme.sandcastle.internal" {
		t.Fatalf("wildcard DNSName = %q", cert.DNSNames[1])
	}
	if cert.DNSNames[2] != "app.example.test" {
		t.Fatalf("extra DNSName = %q", cert.DNSNames[2])
	}
}

func TestMachineDNSNamesIncludesShortProjectHostnames(t *testing.T) {
	names := MachineDNSNames("codex.default", "acme", []string{"app.example.test"})
	want := []string{
		"codex.default.acme",
		"*.codex.default.acme",
		"codex.default",
		"*.codex.default",
		"app.example.test",
	}
	if len(names) != len(want) {
		t.Fatalf("names = %#v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names[%d] = %q, want %q; all names = %#v", i, names[i], want[i], names)
		}
	}
}

func TestIssueMachineLeafRequiresSAN(t *testing.T) {
	now := time.Now()
	ca, err := GenerateCA("Sandcastle CA", now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = IssueMachineLeaf(ca.CertificatePEM, ca.PrivateKeyPEM, "", nil, now)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGenerateSelfSignedServer(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	keyPair, err := GenerateSelfSignedServer("route broker", []string{"sc-route-broker"}, now)
	if err != nil {
		t.Fatal(err)
	}
	cert := parseLeafForTest(t, keyPair.CertificatePEM)
	if cert.IsCA {
		t.Fatal("server certificate should not be a CA")
	}
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Fatalf("ExtKeyUsage = %#v", cert.ExtKeyUsage)
	}
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != "sc-route-broker" {
		t.Fatalf("DNSNames = %#v", cert.DNSNames)
	}
}

func parseLeafForTest(t *testing.T, data []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("missing PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
