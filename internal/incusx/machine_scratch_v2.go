package incusx

import (
	"fmt"
	"net/http"

	"github.com/lxc/incus/v6/shared/api"

	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

// instanceScratchVolume returns the pool and volume the instance's own
// `scratch` disk device attaches. The device, not a recomputed name, is what
// says which volume belongs to the machine.
func instanceScratchVolume(instance *api.Instance) (pool string, volume string, ok bool) {
	if instance == nil {
		return "", "", false
	}
	device, found := instance.Devices[tenant.ScratchDeviceName]
	if !found || device["type"] != "disk" || device["source"] == "" || device["pool"] == "" {
		return "", "", false
	}
	return device["pool"], device["source"], true
}

type scratchVolumeDeleter interface {
	GetStoragePoolVolume(pool string, volType string, name string) (*api.StorageVolume, string, error)
	DeleteStoragePoolVolume(pool string, volType string, name string) error
}

// deleteMachineScratchVolume deletes a machine's scratch volume once its
// instance is gone. A volume without the scratch marker is left alone: only
// Sandcastle-created scratch data is disposable. A missing volume is fine.
func deleteMachineScratchVolume(server scratchVolumeDeleter, pool string, name string) error {
	volume, _, err := server.GetStoragePoolVolume(pool, "custom", name)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}
		return fmt.Errorf("get scratch volume %s: %w", name, err)
	}
	if !tenant.IsScratchVolume(volume.Config) {
		return nil
	}
	if err := ignoreNotFound(server.DeleteStoragePoolVolume(pool, "custom", name)); err != nil {
		return fmt.Errorf("delete scratch volume %s: %w", name, err)
	}
	return nil
}

// deleteScratchVolumes removes every marked scratch volume left in a project
// whose instances are already deleted, so the project itself can be deleted.
func (d TenantDeleter) deleteScratchVolumes(server TenantDeleteResourceServer, projectName string, pool string) error {
	volumes, err := server.GetStoragePoolVolumes(pool)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) || api.StatusErrorCheck(err, http.StatusForbidden) {
			return nil
		}
		return fmt.Errorf("list volumes in project %s: %w", projectName, err)
	}
	for _, volume := range volumes {
		if volume.Type != "custom" || !tenant.IsScratchVolume(volume.Config) {
			continue
		}
		d.log("delete scratch volume " + volume.Name + " in " + projectName)
		if err := ignoreNotFound(server.DeleteStoragePoolVolume(pool, "custom", volume.Name)); err != nil {
			return fmt.Errorf("delete scratch volume %s in %s: %w", volume.Name, projectName, err)
		}
	}
	return nil
}
