package cli

import (
	"context"
	"testing"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

// fakeInstallMachineStore returns each install's machines keyed by the
// summary's infra project name (<prefix>-<tenant>), which is what
// distinguishes same-named tenants of different installs.
type fakeInstallMachineStore struct {
	byInfraProject map[string][]meta.Machine
}

func (s fakeInstallMachineStore) ListMachines(ctx context.Context, summary tenant.Summary) ([]meta.Machine, error) {
	return s.byInfraProject[summary.InfraProject], nil
}

var _ machine.Store = fakeInstallMachineStore{}

// Two installs share one Incus daemon and both have a tenant named "acme".
// `sc list` must resolve the tenant of the install the configured remote
// belongs to — matching by tenant name alone can land on the OTHER install's
// same-named tenant, making a machine created seconds earlier invisible.
func TestListMachinesScopedToCurrentInstall(t *testing.T) {
	v2Project := func(name string) tenant.IncusProject {
		return tenant.IncusProject{Name: name, Config: map[string]string{
			meta.KeyKind:    meta.KindV2Project,
			meta.KeyVersion: "2",
			meta.KeyTenant:  "acme",
		}}
	}
	store := tenant.MemoryStore{Projects: []tenant.IncusProject{
		// "id"-install tenant sorts ahead of the sc2 one, so an unscoped
		// name-only match would pick it.
		v2Project("id-acme-default"),
		v2Project("sc2-acme-default"),
	}}
	machines := fakeInstallMachineStore{byInfraProject: map[string][]meta.Machine{
		"sc2-acme": {{Name: "web", Project: "default", Running: true}},
		"id-acme":  {},
	}}

	tests := []struct {
		name         string
		remote       string
		wantInfra    string
		wantMachines int
	}{
		{"default-prefix remote sees its own machine", "sc-acme", "sc2-acme", 1},
		{"prefixed remote sees its own (empty) install", "sc-id-acme", "id-acme", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := commandConfig{
				adminConfig:  scconfig.Admin{Tenant: "acme", Remote: tt.remote},
				tenantStore:  store,
				machineStore: machines,
			}
			result, err := listMachines(context.Background(), config, listMachinesRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if result.Tenant.InfraProject != tt.wantInfra {
				t.Fatalf("resolved tenant of install %q, want %q", result.Tenant.InfraProject, tt.wantInfra)
			}
			if len(result.Machines) != tt.wantMachines {
				t.Fatalf("got %d machines, want %d: %+v", len(result.Machines), tt.wantMachines, result.Machines)
			}
		})
	}
}
