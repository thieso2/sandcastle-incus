package incusx

import (
	"context"
	"maps"
	"testing"

	"github.com/lxc/incus/v6/shared/api"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

func scratchMigrationFixture() (*fakeScratchResource, TenantCreator) {
	resource := newFakeScratchResource("tank")
	handMade := map[string]string{"type": "disk", "pool": "tank", "source": "my-own-tmp", "path": "/scratch"}
	resource.instances = map[string]*api.Instance{
		"web":     {Name: "web", Type: "container", StatusCode: api.Running},
		"vm1":     {Name: "vm1", Type: "virtual-machine", StatusCode: api.Running},
		"cold":    {Name: "cold", Type: "virtual-machine", StatusCode: api.Stopped},
		"custom":  {Name: "custom", Type: "container", StatusCode: api.Running, InstancePut: api.InstancePut{Devices: map[string]map[string]string{tenant.ScratchDeviceName: handMade}}},
		"sidecar": {Name: "sidecar", Type: "container", StatusCode: api.Running, InstancePut: api.InstancePut{Config: map[string]string{meta.KeyKind: meta.KindSidecar, meta.KeyVersion: "2"}}},
	}
	server := &fakeScratchServer{
		projects: map[string]*api.Project{
			"sc2-acme":         {Name: "sc2-acme", ProjectPut: api.ProjectPut{Config: map[string]string{meta.KeyKind: meta.KindInfra, meta.KeyTenant: "acme"}}},
			"sc2-acme-default": {Name: "sc2-acme-default", ProjectPut: api.ProjectPut{Config: map[string]string{meta.KeyKind: meta.KindV2Project, meta.KeyTenant: "acme"}}},
		},
		resources: map[string]*fakeScratchResource{"sc2-acme-default": resource},
	}
	return resource, TenantCreator{Server: server}
}

func statesByMachine(statuses []ScratchMigrationStatus) map[string]ScratchMigrationState {
	states := map[string]ScratchMigrationState{}
	for _, status := range statuses {
		states[status.Machine] = status.State
	}
	return states
}

func TestScratchMigrationAttachesVolumesOnce(t *testing.T) {
	resource, creator := scratchMigrationFixture()
	statuses, err := creator.MigrateTenantScratch(context.Background(), "", "acme", false)
	if err != nil {
		t.Fatalf("MigrateTenantScratch: %v", err)
	}
	want := map[string]ScratchMigrationState{
		"web":    ScratchAttachedLive,
		"vm1":    ScratchAttachedNextRestart,
		"cold":   ScratchAttachedNextStart,
		"custom": ScratchPresent,
	}
	if got := statesByMachine(statuses); !maps.Equal(got, want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
	for _, machine := range []string{"web", "vm1", "cold"} {
		spec := tenant.MachineScratch{Machine: machine, Pool: "tank", Size: tenant.DefaultScratchSize}
		if volume := resource.volumes[spec.VolumeName()]; volume == nil || !tenant.IsScratchVolume(volume.Config) || volume.Config["size"] != tenant.DefaultScratchSize {
			t.Fatalf("%s: scratch volume = %+v", machine, volume)
		}
		if !maps.Equal(resource.instances[machine].Devices[tenant.ScratchDeviceName], spec.Device()) {
			t.Fatalf("%s: scratch device = %v", machine, resource.instances[machine].Devices)
		}
	}
	if resource.instances["custom"].Devices[tenant.ScratchDeviceName]["source"] != "my-own-tmp" {
		t.Fatal("an existing scratch device was replaced")
	}
	if resource.instances["sidecar"].Devices != nil || resource.volumes["sidecar-scratch"] != nil {
		t.Fatal("infrastructure was migrated")
	}

	updates, volumes := len(resource.updatedInstances), len(resource.createdVolumes)
	again, err := creator.MigrateTenantScratch(context.Background(), "", "acme", false)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	for _, status := range again {
		if status.State != ScratchPresent {
			t.Fatalf("second run changed %s: %s", status.Machine, status.State)
		}
	}
	if len(resource.updatedInstances) != updates || len(resource.createdVolumes) != volumes {
		t.Fatal("second run wrote to Incus")
	}
}

func TestScratchMigrationCheckWritesNothing(t *testing.T) {
	resource, creator := scratchMigrationFixture()
	statuses, err := creator.MigrateTenantScratch(context.Background(), "", "acme", true)
	if err != nil {
		t.Fatalf("MigrateTenantScratch --check: %v", err)
	}
	if got := statesByMachine(statuses); got["web"] != ScratchPlanned || got["vm1"] != ScratchPlanned || got["custom"] != ScratchPresent {
		t.Fatalf("check states = %v", got)
	}
	if len(resource.createdVolumes) != 0 || len(resource.updatedInstances) != 0 {
		t.Fatal("--check wrote to Incus")
	}
}

func TestScratchMigrationReportsAForeignVolumeAndContinues(t *testing.T) {
	resource, creator := scratchMigrationFixture()
	resource.volumes["vm1-scratch"] = &api.StorageVolume{Name: "vm1-scratch", Type: "custom", StorageVolumePut: api.StorageVolumePut{Config: map[string]string{}}}
	statuses, err := creator.MigrateTenantScratch(context.Background(), "", "acme", false)
	if err == nil {
		t.Fatal("want an error for the machine that could not be migrated")
	}
	got := statesByMachine(statuses)
	if got["vm1"] != ScratchFailed || got["web"] != ScratchAttachedLive {
		t.Fatalf("states = %v, want vm1 failed and web still attached", got)
	}
	if resource.instances["vm1"].Devices != nil {
		t.Fatal("vm1 got a device pointing at a foreign volume")
	}
}
