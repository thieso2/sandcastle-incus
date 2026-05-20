package dns

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/thieso2/sandcastle-incus/internal/meta"
)

type Applier interface {
	Apply(context.Context, Project) (ApplyResult, error)
}

type Project struct {
	IncusName   string `json:"incusName"`
	Owner       string `json:"owner"`
	Name        string `json:"name"`
	Domain      string `json:"domain"`
	PrivateCIDR string `json:"privateCIDR"`
}

type ApplyResult struct {
	Project     Project        `json:"project"`
	DNSAddress  string         `json:"dnsAddress"`
	Sandboxes   []meta.Sandbox `json:"sandboxes"`
	Files       []File         `json:"files"`
	RecordCount int            `json:"recordCount"`
}

func PlanApply(summary Project, sandboxes []meta.Sandbox) (ApplyResult, error) {
	dnsAddress, err := dnsAddress(summary.PrivateCIDR)
	if err != nil {
		return ApplyResult{}, err
	}
	files, err := RenderProject(summary.Domain, dnsAddress, sandboxes)
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{
		Project:     summary,
		DNSAddress:  dnsAddress,
		Sandboxes:   sandboxes,
		Files:       files,
		RecordCount: 2 + len(sandboxes)*2,
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
