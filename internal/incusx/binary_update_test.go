package incusx

import (
	"testing"

	"github.com/lxc/incus/v6/shared/api"

	"github.com/thieso2/sandcastle-incus/internal/meta"
)

func fullInstance(project, name, kind, tenant, binaryVersion, status string) api.InstanceFull {
	cfg := map[string]string{meta.KeyKind: kind}
	if tenant != "" {
		cfg[meta.KeyTenant] = tenant
	}
	if binaryVersion != "" {
		cfg[meta.KeyBinaryVersion] = binaryVersion
	}
	return api.InstanceFull{Instance: api.Instance{
		Name:        name,
		Project:     project,
		Status:      status,
		InstancePut: api.InstancePut{Config: cfg},
	}}
}

func TestClassifyComponentsFiltersAndMaps(t *testing.T) {
	instances := []api.InstanceFull{
		fullInstance("infrastructure", "sc2-auth-app", "auth-app", "", "v0.2.0", "Running"),
		fullInstance("sc2-broker", "sc2-broker", "broker", "", "", "Stopped"),
		fullInstance("sc2-acme", "sidecar", "sidecar", "acme", "v0.1.0", "Running"),
		fullInstance("sc2-acme-default", "web", "machine", "acme", "", "Running"),
		{Instance: api.Instance{Name: "unrelated", Project: "default", Status: "Running"}},
	}
	got := classifyComponents(instances)
	if len(got) != 3 {
		t.Fatalf("expected 3 components, got %d: %+v", len(got), got)
	}
	authApp := got[0]
	if authApp.Kind != "auth-app" || authApp.Instance != "sc2-auth-app" || authApp.BinaryVersion != "v0.2.0" ||
		authApp.Status != "Running" || authApp.TenantManaged {
		t.Fatalf("auth-app row wrong: %+v", authApp)
	}
	broker := got[1]
	if broker.Kind != "broker" || broker.BinaryVersion != "" || broker.Status != "Stopped" {
		t.Fatalf("broker row wrong: %+v", broker)
	}
	sidecar := got[2]
	if sidecar.Kind != "sidecar" || sidecar.Tenant != "acme" || !sidecar.TenantManaged ||
		sidecar.Project != "sc2-acme" {
		t.Fatalf("sidecar row wrong: %+v", sidecar)
	}
}
