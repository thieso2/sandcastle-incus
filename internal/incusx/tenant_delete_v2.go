package incusx

import (
	"context"
	"fmt"
	"net/http"

	"github.com/lxc/incus/v6/shared/api"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

// DeleteTenantV2 tears down a v2 tenant completely: every app project
// (machines, images, shared home/workspace volumes, profiles), the infra
// project (sidecar), and the tenant bridge. v2 deletion is all-or-nothing —
// the shared volumes live inside the app projects, so there is no meaningful
// "runtime only" subset (PlanDeleteV2 enforces --purge).
func (d TenantDeleter) DeleteTenantV2(ctx context.Context, plan tenant.DeletePlanV2) error {
	server := d.Server
	if server == nil {
		loaded, err := LoadCLIConfig(d.ConfigPath)
		if err != nil {
			return fmt.Errorf("load Incus config: %w", err)
		}
		remote := d.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := connectInstanceServer(loaded, remote)
		if err != nil {
			return fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkDeleteServer{inner: instanceServer}
	}
	for _, project := range plan.AppProjects {
		if err := d.deleteV2AppProject(server, project, plan.StoragePool, plan.DurableVolumes); err != nil {
			return err
		}
	}
	// The infra project holds only the sidecar (and its profile) — no volumes.
	d.log("purge infra project " + plan.InfraProject)
	if err := d.deleteProjectCompletely(server, plan.InfraProject); err != nil {
		return err
	}
	// The tenant bridge lives in the default project; every profile that
	// referenced it is gone with the projects above (a live reference — or a
	// leftover dnsmasq — would hold the gateway IP, see the e2e appendix).
	d.log("delete tenant bridge " + plan.Bridge)
	if err := ignoreNotFound(server.UseProject("default").DeleteNetwork(plan.Bridge)); err != nil {
		return fmt.Errorf("delete tenant bridge %s: %w", plan.Bridge, err)
	}
	d.log("done")
	return nil
}

// deleteV2AppProject removes one v2 app project and everything in it. The
// shared volumes are detached from the default profile first — a project
// cannot be deleted while volumes exist, and volumes cannot be deleted while
// a profile references them.
func (d TenantDeleter) deleteV2AppProject(server TenantDeleteServer, projectName string, pool string, volumes []string) error {
	projectServer := server.UseProject(projectName)
	instances, err := projectServer.GetInstances(api.InstanceTypeAny)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) || api.StatusErrorCheck(err, http.StatusForbidden) {
			return nil
		}
		return fmt.Errorf("list instances in project %s: %w", projectName, err)
	}
	for _, instance := range instances {
		d.log("delete instance " + instance.Name + " in " + projectName)
		if err := deleteInstance(projectServer, instance.Name); err != nil {
			return err
		}
	}
	if err := d.clearVolumeDevicesFromDefaultProfile(projectServer, projectName, volumes); err != nil {
		return err
	}
	for _, volume := range volumes {
		d.log("delete shared volume " + volume + " in " + projectName)
		if err := ignoreNotFound(projectServer.DeleteStoragePoolVolume(pool, "custom", volume)); err != nil {
			return fmt.Errorf("delete volume %s in %s: %w", volume, projectName, err)
		}
	}
	return d.deleteProjectCompletely(server, projectName)
}

func (d TenantDeleter) clearVolumeDevicesFromDefaultProfile(server TenantDeleteResourceServer, projectName string, volumes []string) error {
	profile, etag, err := server.GetProfile("default")
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	named := map[string]bool{}
	for _, volume := range volumes {
		named[volume] = true
	}
	changed := false
	for key, device := range profile.Devices {
		if device["type"] == "disk" && named[device["source"]] {
			delete(profile.Devices, key)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	d.log("detach shared volumes from default profile in " + projectName)
	return server.UpdateProfile("default", profile.Writable(), etag)
}
