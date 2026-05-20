package route

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
)

type DNSResolver interface {
	LookupHost(context.Context, string) ([]string, error)
}

type NetResolver struct{}

func (NetResolver) LookupHost(ctx context.Context, hostname string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, hostname)
}

func VerifyDNSProof(ctx context.Context, resolver DNSResolver, proof DNSProof) (DNSProof, error) {
	if !proof.Required {
		return proof, nil
	}
	if strings.TrimSpace(proof.ExpectedTarget) == "" {
		return proof, fmt.Errorf("infrastructure DNS proof target is not configured")
	}
	if resolver == nil {
		resolver = NetResolver{}
	}
	resolved, err := resolver.LookupHost(ctx, proof.Hostname)
	if err != nil {
		return proof, fmt.Errorf("resolve public route hostname %s: %w", proof.Hostname, err)
	}
	normalizedExpected := normalizeDNSTarget(proof.ExpectedTarget)
	for _, target := range resolved {
		if normalizeDNSTarget(target) == normalizedExpected {
			proof.ResolvedTargets = sortedTargets(resolved)
			return proof, nil
		}
	}
	proof.ResolvedTargets = sortedTargets(resolved)
	return proof, fmt.Errorf("public route hostname %s resolves to %s, want %s", proof.Hostname, strings.Join(proof.ResolvedTargets, ","), proof.ExpectedTarget)
}

func normalizeDNSTarget(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func sortedTargets(values []string) []string {
	output := append([]string(nil), values...)
	sort.Strings(output)
	return output
}
