package cli

import (
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

// `sc status` share counts are computed client-side from Summary.StorageShares,
// which the tenant list leaves empty, and the inbound counts need every other
// tenant's registry — so the counts were always zero. They now come from the
// Auth App's list endpoints.
func TestStatusShowsShareCountsFromAuthApp(t *testing.T) {
	share := func(source, project, name string) meta.TenantStorageShare {
		return meta.TenantStorageShare{SourceTenant: source, SourceProject: project, Name: name}
	}
	client := &fakeAuthShareClient{
		shares:        []meta.TenantStorageShare{share("acme", "default", "s1"), share("acme", "backend", "s2")},
		inboundShares: []meta.TenantStorageShare{share("other", "default", "in1")},
		offers:        []meta.TenantStorageShare{share("other", "default", "o1"), share("third", "default", "o2"), share("fourth", "default", "o3")},
	}
	stdout, err := executeForTestWithConfig(t, commandConfig{
		name:         "sandcastle",
		tenantStore:  tenant.MemoryStore{Projects: v2TenantProjects("acme", "10.61.0.0/24", "default")},
		machineStore: fakeMachineStatusStore{},
		authShares:   client,
	}, "status", "acme")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Outbound shares: 2",
		"Inbound accepted shares: 1",
		"Pending inbound share offers: 3",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status missing %q:\n%s", want, stdout)
		}
	}
}
