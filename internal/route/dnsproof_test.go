package route

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeDNSResolver struct {
	hosts []string
	err   error
}

func (r fakeDNSResolver) LookupHost(ctx context.Context, hostname string) ([]string, error) {
	return r.hosts, r.err
}

type mappedDNSResolver struct {
	hosts     map[string][]string
	errByHost map[string]error
}

func (r mappedDNSResolver) LookupHost(ctx context.Context, hostname string) ([]string, error) {
	if err := r.errByHost[hostname]; err != nil {
		return nil, err
	}
	return r.hosts[hostname], nil
}

func TestVerifyDNSProofAcceptsExpectedTarget(t *testing.T) {
	proof, err := VerifyDNSProof(context.Background(), fakeDNSResolver{hosts: []string{"203.0.113.10"}}, DNSProof{
		Required:       true,
		Hostname:       "app.example.com",
		ExpectedTarget: "203.0.113.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(proof.ResolvedTargets) != 1 || proof.ResolvedTargets[0] != "203.0.113.10" {
		t.Fatalf("ResolvedTargets = %#v", proof.ResolvedTargets)
	}
}

func TestVerifyDNSProofAcceptsExpectedHostnameTarget(t *testing.T) {
	proof, err := VerifyDNSProof(context.Background(), mappedDNSResolver{hosts: map[string][]string{
		"app.example.com":   {"203.0.113.10"},
		"infra.example.com": {"203.0.113.10"},
	}}, DNSProof{
		Required:       true,
		Hostname:       "app.example.com",
		ExpectedTarget: "Infra.Example.COM.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(proof.ResolvedTargets) != 1 || proof.ResolvedTargets[0] != "203.0.113.10" {
		t.Fatalf("ResolvedTargets = %#v", proof.ResolvedTargets)
	}
}

func TestVerifyDNSProofRejectsUnresolvedExpectedHostnameTarget(t *testing.T) {
	_, err := VerifyDNSProof(context.Background(), mappedDNSResolver{
		hosts:     map[string][]string{"app.example.com": {"203.0.113.10"}},
		errByHost: map[string]error{"infra.example.com": errors.New("boom")},
	}, DNSProof{
		Required:       true,
		Hostname:       "app.example.com",
		ExpectedTarget: "infra.example.com",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "resolve infrastructure DNS proof target") {
		t.Fatalf("error = %q", err)
	}
}

func TestVerifyDNSProofRejectsMismatchedExpectedHostnameTarget(t *testing.T) {
	_, err := VerifyDNSProof(context.Background(), mappedDNSResolver{hosts: map[string][]string{
		"app.example.com":   {"203.0.113.10"},
		"infra.example.com": {"203.0.113.11"},
	}}, DNSProof{
		Required:       true,
		Hostname:       "app.example.com",
		ExpectedTarget: "infra.example.com",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVerifyDNSProofRejectsMissingExpectedTarget(t *testing.T) {
	_, err := VerifyDNSProof(context.Background(), fakeDNSResolver{hosts: []string{"203.0.113.10"}}, DNSProof{
		Required: true,
		Hostname: "app.example.com",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %q", err)
	}
}

func TestVerifyDNSProofRejectsMismatchedTarget(t *testing.T) {
	_, err := VerifyDNSProof(context.Background(), fakeDNSResolver{hosts: []string{"203.0.113.11"}}, DNSProof{
		Required:       true,
		Hostname:       "app.example.com",
		ExpectedTarget: "203.0.113.10",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestVerifyDNSProofWrapsResolverError(t *testing.T) {
	_, err := VerifyDNSProof(context.Background(), fakeDNSResolver{err: errors.New("boom")}, DNSProof{
		Required:       true,
		Hostname:       "app.example.com",
		ExpectedTarget: "203.0.113.10",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "resolve public route hostname") {
		t.Fatalf("error = %q", err)
	}
}
