package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"time"
)

type KeyPair struct {
	CertificatePEM []byte
	PrivateKeyPEM  []byte
}

func GenerateCA(commonName string, now time.Time) (KeyPair, error) {
	if strings.TrimSpace(commonName) == "" {
		return KeyPair{}, fmt.Errorf("common name is required")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return KeyPair{}, err
	}
	template := &x509.Certificate{
		SerialNumber:          serialNumber(),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	return createCertificate(template, template, key, key)
}

func GenerateSelfSignedServer(commonName string, dnsNames []string, now time.Time) (KeyPair, error) {
	names := normalizeNames(dnsNames)
	if len(names) == 0 {
		return KeyPair{}, fmt.Errorf("at least one DNS SAN is required")
	}
	if strings.TrimSpace(commonName) == "" {
		commonName = names[0]
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return KeyPair{}, err
	}
	template := &x509.Certificate{
		SerialNumber: serialNumber(),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     names,
	}
	return createCertificate(template, template, key, key)
}

func IssueSandboxLeaf(caCertPEM []byte, caKeyPEM []byte, commonName string, dnsNames []string, now time.Time) (KeyPair, error) {
	caCert, err := parseCertificate(caCertPEM)
	if err != nil {
		return KeyPair{}, err
	}
	caKey, err := parsePrivateKey(caKeyPEM)
	if err != nil {
		return KeyPair{}, err
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return KeyPair{}, err
	}
	names := normalizeNames(dnsNames)
	if len(names) == 0 {
		return KeyPair{}, fmt.Errorf("at least one DNS SAN is required")
	}
	if commonName == "" {
		commonName = names[0]
	}
	template := &x509.Certificate{
		SerialNumber: serialNumber(),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     names,
	}
	return createCertificate(template, caCert, leafKey, caKey)
}

func SandboxDNSNames(name string, domain string, extra []string) []string {
	base := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	sandbox := strings.ToLower(strings.TrimSpace(name))
	names := []string{
		sandbox + "." + base,
		"*." + sandbox + "." + base,
	}
	return append(names, normalizeNames(extra)...)
}

func createCertificate(template *x509.Certificate, parent *x509.Certificate, key *ecdsa.PrivateKey, parentKey *ecdsa.PrivateKey) (KeyPair, error) {
	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, parentKey)
	if err != nil {
		return KeyPair{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return KeyPair{}, err
	}
	return KeyPair{
		CertificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		PrivateKeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}, nil
}

func parseCertificate(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("certificate PEM is required")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parsePrivateKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("private key PEM is required")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

func normalizeNames(names []string) []string {
	seen := map[string]bool{}
	output := []string{}
	for _, name := range names {
		normalized := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		output = append(output, normalized)
	}
	return output
}

func serialNumber() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	value, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return value
}
