package tenant

import (
	"context"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
)

func TestListManagedTenants(t *testing.T) {
	acmeConfig, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		PrivateCIDR: "10.248.0.0/24",
		Projects:    []meta.Project{{Name: "default"}, {Name: "website"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	zeusConfig, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "zeus",
		PrivateCIDR: "10.248.1.0/24",
		Projects:    []meta.Project{{Name: "default"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := MemoryStore{Projects: []IncusProject{
		{Name: "default", Config: map[string]string{}},
		{Name: "sc-infra", Config: map[string]string{meta.KeyKind: "infrastructure", meta.KeyVersion: "1"}},
		{Name: "sc-zeus", Config: zeusConfig},
		{Name: "sc-acme", Config: acmeConfig},
	}}

	summaries, err := List(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("len(summaries) = %d, want 2", len(summaries))
	}
	if summaries[0].Tenant != "acme" || len(summaries[0].Projects) != 2 {
		t.Fatalf("first summary = %#v", summaries[0])
	}
	if summaries[1].Tenant != "zeus" || len(summaries[1].Projects) != 1 {
		t.Fatalf("second summary = %#v", summaries[1])
	}
}

func TestListReportsInvalidManagedMetadata(t *testing.T) {
	store := MemoryStore{Projects: []IncusProject{{
		Name: "sc-broken",
		Config: map[string]string{
			meta.KeyKind:    meta.KindTenant,
			meta.KeyVersion: "1",
		},
	}}}

	_, err := List(context.Background(), store)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAllocatedCIDRsSpansV1AndV2(t *testing.T) {
	v1Config, err := meta.TenantConfig(meta.Tenant{Tenant: "acme", PrivateCIDR: "10.248.0.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	store := MemoryStore{Projects: []IncusProject{
		{Name: "default", Config: map[string]string{}},                                                       // unmanaged
		{Name: "sc-acme", Config: v1Config},                                                                  // v1 tenant
		{Name: "sc2-zeus", Config: map[string]string{meta.KeyKind: meta.KindInfra, meta.KeyVersion: "2", meta.KeyV2CIDR: "10.249.1.0/24"}}, // v2 infra
		{Name: "sc2-zeus-default", Config: map[string]string{meta.KeyKind: "project", meta.KeyVersion: "2"}}, // v2 app project: no CIDR
	}}

	cidrs, err := AllocatedCIDRs(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range cidrs {
		got[c] = true
	}
	if !got["10.248.0.0/24"] || !got["10.249.1.0/24"] {
		t.Fatalf("AllocatedCIDRs = %#v, want both v1 10.248.0.0/24 and v2 10.249.1.0/24", cidrs)
	}
	if len(cidrs) != 2 {
		t.Fatalf("len(cidrs) = %d, want 2 (no CIDR from unmanaged/app projects)", len(cidrs))
	}
}

func TestCIDRAllocationInputsSplitsOwnFromOthers(t *testing.T) {
	acme, err := meta.TenantConfig(meta.Tenant{Tenant: "acme", PrivateCIDR: "10.248.0.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	store := MemoryStore{Projects: []IncusProject{
		{Name: "sc-acme", Config: acme}, // v1, other tenant
		{Name: "sc2-zeus", Config: map[string]string{meta.KeyKind: meta.KindInfra, meta.KeyVersion: "2", meta.KeyTenant: "zeus", meta.KeyV2CIDR: "10.249.0.0/24"}},
		{Name: "sc2-hera", Config: map[string]string{meta.KeyKind: meta.KindInfra, meta.KeyVersion: "2", meta.KeyTenant: "hera", meta.KeyV2CIDR: "10.249.1.0/24"}},
	}}

	// Existing tenant → own CIDR returned, its own CIDR excluded from others.
	own, others, err := CIDRAllocationInputs(context.Background(), store, "zeus")
	if err != nil {
		t.Fatal(err)
	}
	if own != "10.249.0.0/24" {
		t.Fatalf("own = %q, want 10.249.0.0/24", own)
	}
	for _, c := range others {
		if c == "10.249.0.0/24" {
			t.Fatalf("others %#v must not contain zeus's own CIDR", others)
		}
	}
	if len(others) != 2 {
		t.Fatalf("len(others) = %d, want 2 (acme + hera)", len(others))
	}

	// New tenant → no own CIDR, all three are occupied.
	own, others, err = CIDRAllocationInputs(context.Background(), store, "newbie")
	if err != nil {
		t.Fatal(err)
	}
	if own != "" || len(others) != 3 {
		t.Fatalf("newbie: own=%q others=%#v, want own empty and 3 others", own, others)
	}
}

func TestOccupiedCIDRs(t *testing.T) {
	cidrs := OccupiedCIDRs([]Summary{
		{PrivateCIDR: "10.248.0.0/24"},
		{},
		{PrivateCIDR: "10.248.1.0/24"},
	})
	if len(cidrs) != 2 {
		t.Fatalf("len(cidrs) = %d, want 2", len(cidrs))
	}
	if cidrs[0] != "10.248.0.0/24" || cidrs[1] != "10.248.1.0/24" {
		t.Fatalf("cidrs = %#v", cidrs)
	}
}
