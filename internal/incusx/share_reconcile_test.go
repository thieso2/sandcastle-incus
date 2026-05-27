package incusx

import (
	"context"
	"errors"
	"testing"

	"github.com/gorilla/websocket"
	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/share"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type fakeShareReconcileStore struct {
	machines []meta.Machine
}

func (s fakeShareReconcileStore) ListMachines(ctx context.Context, summary tenant.Summary) ([]meta.Machine, error) {
	return append([]meta.Machine{}, s.machines...), nil
}

type fakeShareReconcileServer struct {
	resource *fakeShareReconcileResource
}

func (s fakeShareReconcileServer) UseProject(name string) ShareReconcileResourceServer {
	return s.resource
}

type fakeShareReconcileResource struct {
	instance  *api.Instance
	updated   *api.InstancePut
	updateErr error
	waitErr   error
}

func (r *fakeShareReconcileResource) GetInstance(name string) (*api.Instance, string, error) {
	return r.instance, "etag", nil
}

func (r *fakeShareReconcileResource) UpdateInstance(name string, instance api.InstancePut, etag string) (incus.Operation, error) {
	r.updated = &instance
	if r.updateErr != nil {
		return nil, r.updateErr
	}
	return failingOperation{err: r.waitErr}, nil
}

type failingOperation struct {
	err error
}

func (o failingOperation) AddHandler(func(api.Operation)) (*incus.EventTarget, error) {
	return nil, nil
}
func (o failingOperation) Cancel() error                                { return nil }
func (o failingOperation) Get() api.Operation                           { return api.Operation{} }
func (o failingOperation) GetWebsocket(string) (*websocket.Conn, error) { return nil, nil }
func (o failingOperation) RemoveHandler(*incus.EventTarget) error       { return nil }
func (o failingOperation) Refresh() error                               { return nil }
func (o failingOperation) Wait() error                                  { return o.err }
func (o failingOperation) WaitContext(context.Context) error            { return o.err }

func TestShareReconcilerAddsAcceptedShareDevice(t *testing.T) {
	resource := &fakeShareReconcileResource{instance: &api.Instance{
		InstancePut: api.InstancePut{Devices: map[string]map[string]string{
			"root": {"type": "disk", "path": "/"},
		}},
	}}
	result, err := shareReconcilerForTest(resource).ReconcileTenantShares(context.Background(), tenantSummaryWithShare(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Machines) != 1 || result.Machines[0].Status != "updated" || !result.Machines[0].Changed {
		t.Fatalf("result = %#v", result)
	}
	if resource.updated == nil {
		t.Fatal("expected instance update")
	}
	device := resource.updated.Devices[share.DeviceName(acceptedShare())]
	if device["readonly"] != "true" || device["pool"] != "sc-thieso2" || device["path"] != "/shared/thieso2/default/docs" {
		t.Fatalf("device = %#v", device)
	}
}

func TestShareReconcilerIsIdempotent(t *testing.T) {
	storageShare := acceptedShare()
	resource := &fakeShareReconcileResource{instance: &api.Instance{
		InstancePut: api.InstancePut{Devices: map[string]map[string]string{
			share.DeviceName(storageShare): share.DesiredDevice(storageShare, "sc-thieso2", tenant.WorkspaceVolumeName),
		}},
	}}
	result, err := shareReconcilerForTest(resource).ReconcileTenantShares(context.Background(), tenantSummaryWithShare(), false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Machines[0].Status != "current" || result.Machines[0].Changed {
		t.Fatalf("result = %#v", result)
	}
	if resource.updated != nil {
		t.Fatal("expected no update")
	}
}

func TestShareReconcilerDryRunDoesNotUpdate(t *testing.T) {
	resource := &fakeShareReconcileResource{instance: &api.Instance{InstancePut: api.InstancePut{Devices: map[string]map[string]string{}}}}
	result, err := shareReconcilerForTest(resource).ReconcileTenantShares(context.Background(), tenantSummaryWithShare(), true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Machines[0].Status != "would-update" || !result.Machines[0].Changed {
		t.Fatalf("result = %#v", result)
	}
	if resource.updated != nil {
		t.Fatal("expected no update")
	}
}

func TestShareReconcilerReportsHotplugFailureWithoutRollback(t *testing.T) {
	resource := &fakeShareReconcileResource{
		instance: &api.Instance{InstancePut: api.InstancePut{Devices: map[string]map[string]string{}}},
		waitErr:  errors.New("hotplug refused"),
	}
	result, err := shareReconcilerForTest(resource).ReconcileTenantShares(context.Background(), tenantSummaryWithShare(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasFailures() || result.Machines[0].Status != "failed" {
		t.Fatalf("result = %#v", result)
	}
	if resource.updated == nil {
		t.Fatal("expected attempted update")
	}
}

func shareReconcilerForTest(resource *fakeShareReconcileResource) ShareReconciler {
	return ShareReconciler{
		Admin:  config.Admin{IncusProjectPrefix: config.DefaultIncusProjectPrefix},
		Server: fakeShareReconcileServer{resource: resource},
		Store: fakeShareReconcileStore{machines: []meta.Machine{{
			Tenant:  "skorfman",
			Project: "default",
			Name:    "codex",
			Type:    meta.MachineTypeContainer,
		}}},
	}
}

func tenantSummaryWithShare() tenant.Summary {
	return tenant.Summary{
		Tenant:        "skorfman",
		IncusName:     "sc-skorfman",
		StorageShares: []meta.TenantStorageShare{acceptedShare()},
	}
}

func acceptedShare() meta.TenantStorageShare {
	return meta.TenantStorageShare{
		SourceTenant:  "thieso2",
		SourceProject: "default",
		SourceDir:     "docs",
		Name:          "docs",
		Availability:  share.AvailabilityAvailable,
		Recipients: []meta.TenantStorageShareRecipient{{
			Tenant: "skorfman",
			State:  share.RecipientStateAccepted,
		}},
	}
}
