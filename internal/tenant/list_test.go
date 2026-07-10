package tenant

import (
	"context"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
)

func TestListManagedTenants(t *testing.T) {
	projects := []IncusProject{
		{Name: "default", Config: map[string]string{}},
		{Name: "sc-infra", Config: map[string]string{meta.KeyKind: "infrastructure", meta.KeyVersion: "1"}},
	}
	projects = append(projects, v2ProjectsForTest("acme", "10.248.0.0/24", "default", "website")...)
	projects = append(projects, v2ProjectsForTest("zeus", "10.248.1.0/24")...)
	store := MemoryStore{Projects: projects}

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

func TestAllocatedCIDRsSpansV1AndV2(t *testing.T) {
	v1Config, err := meta.TenantConfig(meta.Tenant{Tenant: "acme", PrivateCIDR: "10.248.0.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	store := MemoryStore{Projects: []IncusProject{
		{Name: "default", Config: map[string]string{}}, // unmanaged
		{Name: "sc-acme", Config: v1Config},            // v1 tenant
		{Name: "sc2-zeus", Config: map[string]string{meta.KeyKind: meta.KindInfra, meta.KeyVersion: "2", meta.KeyV2CIDR: "10.249.1.0/24"}}, // v2 infra
		{Name: "sc2-zeus-default", Config: map[string]string{meta.KeyKind: "project", meta.KeyVersion: "2"}},                               // v2 app project: no CIDR
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

func TestListSurfacesV2Tenants(t *testing.T) {
	store := MemoryStore{Projects: []IncusProject{
		{Name: "sc2-acme", Config: map[string]string{
			meta.KeyKind: meta.KindInfra, meta.KeyTenant: "acme", meta.KeyVersion: "2", meta.KeyV2CIDR: "10.250.0.0/24",
		}},
		{Name: "sc2-acme-default", Config: map[string]string{
			meta.KeyKind: meta.KindV2Project, meta.KeyTenant: "acme", meta.KeyVersion: "2",
		}},
		{Name: "sc2-acme-web", Config: map[string]string{
			meta.KeyKind: meta.KindV2Project, meta.KeyTenant: "acme", meta.KeyVersion: "2",
		}},
		{Name: "unrelated", Config: map[string]string{}},
	}}
	summaries, err := List(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("len(summaries) = %d, want 1: %#v", len(summaries), summaries)
	}
	summary := summaries[0]
	if summary.Tenant != "acme" || summary.Version != 2 {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.IncusName != "sc2-acme-default" {
		t.Fatalf("IncusName = %q, want sc2-acme-default", summary.IncusName)
	}
	if summary.InfraProject != "sc2-acme" {
		t.Fatalf("InfraProject = %q", summary.InfraProject)
	}
	if summary.DNSSuffix != "acme" {
		t.Fatalf("DNSSuffix = %q", summary.DNSSuffix)
	}
	if len(summary.Projects) != 2 || summary.Projects[0].Name != "default" || summary.Projects[1].Name != "web" {
		t.Fatalf("Projects = %#v", summary.Projects)
	}
	if got := summary.V2IncusProjectName("web"); got != "sc2-acme-web" {
		t.Fatalf("V2IncusProjectName(web) = %q", got)
	}
	if got := summary.V2IncusProjectName(""); got != "sc2-acme-default" {
		t.Fatalf("V2IncusProjectName(\"\") = %q", got)
	}
}

// Two sandcastles share one Incus host (--prefix): another install's
// same-named tenant is a FOREIGN tenant — its CIDR is occupied, its suffix
// must not be mistaken for this install's (that false match blocked a second
// install's first login with a bogus suffix-immutability error).
func TestProvisionReuseInputsScopedToInstallPrefix(t *testing.T) {
	store := MemoryStore{Projects: []IncusProject{
		{Name: "sc2-acme", Config: map[string]string{
			meta.KeyKind: meta.KindInfra, meta.KeyVersion: "2", meta.KeyTenant: "acme",
			meta.KeyV2CIDR: "10.253.0.0/24", meta.KeyV2Suffix: "acme", meta.KeyV2Prefix: "sc2",
		}},
		{Name: "id-acme", Config: map[string]string{
			meta.KeyKind: meta.KindInfra, meta.KeyVersion: "2", meta.KeyTenant: "acme",
			meta.KeyV2CIDR: "10.251.0.0/24", meta.KeyV2Suffix: "idefix", meta.KeyV2Prefix: "id",
		}},
	}}
	// the "id" install sees ITS tenant as own, the sc2 one as occupied
	own, suffix, occupied, err := ProvisionReuseInputs(context.Background(), store, "id", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if own != "10.251.0.0/24" || suffix != "idefix" {
		t.Fatalf("id install own = %q/%q", own, suffix)
	}
	if len(occupied) != 1 || occupied[0] != "10.253.0.0/24" {
		t.Fatalf("occupied = %v", occupied)
	}
	// and vice versa (default prefix normalizes sc→sc2)
	own, suffix, occupied, err = ProvisionReuseInputs(context.Background(), store, "sc", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if own != "10.253.0.0/24" || suffix != "acme" {
		t.Fatalf("sc install own = %q/%q", own, suffix)
	}
	if len(occupied) != 1 || occupied[0] != "10.251.0.0/24" {
		t.Fatalf("occupied = %v", occupied)
	}
}

// A v1 (kind=tenant) project with the SAME tenant name is still a foreign
// install's tenant: its /24 must come back as occupied, never as own —
// treating it as own made provisioning reuse the CIDR and collide with the
// live v1 bridge's dnsmasq on the gateway IP ("Address already in use").
func TestProvisionReuseInputsNeverOwnsV1CIDR(t *testing.T) {
	v1Config, err := meta.TenantConfig(meta.Tenant{Tenant: "thieso2", PrivateCIDR: "10.248.1.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	store := MemoryStore{Projects: []IncusProject{
		{Name: "sc-thieso2", Config: v1Config},
	}}
	for _, prefix := range []string{"tc2", "sc", ""} {
		own, suffix, occupied, err := ProvisionReuseInputs(context.Background(), store, prefix, "thieso2")
		if err != nil {
			t.Fatal(err)
		}
		if own != "" || suffix != "" {
			t.Fatalf("prefix %q: own = %q/%q, want empty (v1 CIDR must not be reused)", prefix, own, suffix)
		}
		if len(occupied) != 1 || occupied[0] != "10.248.1.0/24" {
			t.Fatalf("prefix %q: occupied = %v, want the v1 /24", prefix, occupied)
		}
	}
}

// v2ProjectsForTest is the v2 fixture: a kind=infra project carrying the /24,
// plus one kind=project app project per name.
func v2ProjectsForTest(name, cidr string, projects ...string) []IncusProject {
	if len(projects) == 0 {
		projects = []string{"default"}
	}
	cfg := func(kind string) map[string]string {
		out := map[string]string{meta.KeyKind: kind, meta.KeyTenant: name, meta.KeyVersion: "2"}
		if kind == meta.KindInfra && cidr != "" {
			out[meta.KeyV2CIDR] = cidr
		}
		return out
	}
	out := []IncusProject{{Name: "sc2-" + name, Config: cfg(meta.KindInfra)}}
	for _, p := range projects {
		out = append(out, IncusProject{Name: "sc2-" + name + "-" + p, Config: cfg(meta.KindV2Project)})
	}
	return out
}
