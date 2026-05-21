package dns

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/thieso2/sandcastle-incus/internal/meta"
)

type Applier interface {
	Apply(context.Context, Tenant) (ApplyResult, error)
}

type Tenant struct {
	IncusName   string `json:"incusName"`
	Tenant      string `json:"tenant"`
	DNSSuffix   string `json:"dnsSuffix"`
	PrivateCIDR string `json:"privateCIDR"`
}

type ApplyResult struct {
	Tenant      Tenant         `json:"tenant"`
	DNSAddress  string         `json:"dnsAddress"`
	Machines    []meta.Machine `json:"machines"`
	Files       []File         `json:"files"`
	RecordCount int            `json:"recordCount"`
}

func PlanApply(summary Tenant, machines []meta.Machine) (ApplyResult, error) {
	dnsAddress, err := dnsAddress(summary.PrivateCIDR)
	if err != nil {
		return ApplyResult{}, err
	}
	files, err := RenderTenant(summary.DNSSuffix, dnsAddress, machines)
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{
		Tenant:      summary,
		DNSAddress:  dnsAddress,
		Machines:    machines,
		Files:       files,
		RecordCount: 2 + len(machines)*2,
	}, nil
}

func dnsAddress(cidr string) (string, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return "", err
	}
	addr := prefix.Masked().Addr().As4()
	addr[3] = 53
	candidate := netip.AddrFrom4(addr)
	if !prefix.Contains(candidate) {
		return "", fmt.Errorf("DNS address .53 is outside %s", cidr)
	}
	return candidate.String(), nil
}
