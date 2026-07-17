package incusx

import (
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

func devicesTestPlan() tenant.CreatePlanV2 {
	return tenant.CreatePlanV2{
		Tenant:      "acme",
		Bridge:      "sc2-acme",
		StoragePool: "default",
	}
}

// Every machine in a v2 app project mounts the /.sc shared-scripts layers via
// the default profile (spec #127): platform read-only, local read-write —
// the per-layer writability contract, enforced at the disk device.
func TestV2AppProfileDevicesAttachSCVolumes(t *testing.T) {
	devices := v2AppProfileDevices(devicesTestPlan(), true)

	platform, ok := devices["sc-platform"]
	if !ok {
		t.Fatalf("no sc-platform device: %v", devices)
	}
	if platform["source"] != tenant.V2SCPlatformVolumeName || platform["path"] != tenant.SCPlatformPath {
		t.Fatalf("sc-platform device = %v", platform)
	}
	if platform["readonly"] != "true" {
		t.Fatalf("platform layer must be read-only to machines: %v", platform)
	}

	local, ok := devices["sc-local"]
	if !ok {
		t.Fatalf("no sc-local device: %v", devices)
	}
	if local["source"] != tenant.V2SCLocalVolumeName || local["path"] != tenant.SCLocalPath {
		t.Fatalf("sc-local device = %v", local)
	}
	if local["readonly"] != "" {
		t.Fatalf("local layer must be tenant-writable: %v", local)
	}
}

// The /home device stays gated on idmapped-mount support; the /.sc layers do
// not (they hold world-readable scripts, and local mirrors /workspace's
// always-attached behavior).
func TestV2AppProfileDevicesUnshiftedKeepsSCAndWorkspace(t *testing.T) {
	devices := v2AppProfileDevices(devicesTestPlan(), false)
	if _, ok := devices["home"]; ok {
		t.Fatalf("unshifted host must not share /home: %v", devices)
	}
	for _, name := range []string{"workspace", "sc-platform", "sc-local"} {
		if _, ok := devices[name]; !ok {
			t.Fatalf("missing %s device on unshifted host: %v", name, devices)
		}
	}
}
