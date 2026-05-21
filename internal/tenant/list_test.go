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
