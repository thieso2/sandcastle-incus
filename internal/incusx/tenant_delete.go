package incusx

import (
	"context"
	"fmt"
	"io"
	"net/http"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type TenantDeleteServer interface {
	GetProjects() ([]api.Project, error)
	DeleteProject(name string) error
	DeleteStoragePool(name string) error
	UseProject(name string) TenantDeleteResourceServer
}

type TenantDeleteResourceServer interface {
	GetInstances(instanceType api.InstanceType) ([]api.Instance, error)
	GetInstance(name string) (*api.Instance, string, error)
	UpdateInstanceState(name string, state api.InstanceStatePut, ETag string) (incus.Operation, error)
	DeleteInstance(name string) (incus.Operation, error)
	DeleteNetwork(name string) error
	DeleteStoragePoolVolume(pool string, volType string, name string) error
	GetImages() ([]api.Image, error)
	DeleteImage(fingerprint string) (incus.Operation, error)
	GetProfiles() ([]api.Profile, error)
	GetProfile(name string) (*api.Profile, string, error)
	UpdateProfile(name string, profile api.ProfilePut, ETag string) error
	DeleteProfile(name string) error
}

type TenantDeleter struct {
	Remote     string
	ConfigPath string
	Server     TenantDeleteServer
	Log        func(string)
}

func NewTenantDeleter(remote string) TenantDeleter {
	return TenantDeleter{Remote: remote}
}

func (d TenantDeleter) WithVerbose(enabled bool, w io.Writer) TenantDeleter {
	if enabled {
		d.Log = func(msg string) { fmt.Fprintln(w, "[tenant-delete] "+msg) }
	}
	return d
}

func (d TenantDeleter) log(msg string) {
	if d.Log != nil {
		d.Log(msg)
	}
}

func (d TenantDeleter) DeleteTenant(ctx context.Context, plan tenant.DeletePlan) error {
	server := d.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(d.ConfigPath)
		if err != nil {
			return fmt.Errorf("load Incus config: %w", err)
		}
		remote := d.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkDeleteServer{inner: instanceServer}
	}
	// Always purge infra and native projects (they hold no durable state).
	for _, project := range []string{plan.InfraProject, plan.NativeProject} {
		if project == "" {
			continue
		}
		d.log("purge project " + project)
		if err := d.deleteProjectCompletely(server, project); err != nil {
			return err
		}
	}
	projectServer := server.UseProject(plan.IncusProject)
	allInstances, err := projectServer.GetInstances(api.InstanceTypeAny)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("list instances in project %s: %w", plan.IncusProject, err)
	}
	for _, instance := range allInstances {
		d.log("delete instance " + instance.Name)
		if err := deleteInstance(projectServer, instance.Name); err != nil {
			return err
		}
	}
	profiles, err := projectServer.GetProfiles()
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("list profiles in project %s: %w", plan.IncusProject, err)
	}
	for _, profile := range profiles {
		if profile.Name == "default" {
			continue
		}
		d.log("delete project profile " + profile.Name)
		if err := ignoreNotFound(projectServer.DeleteProfile(profile.Name)); err != nil {
			return fmt.Errorf("delete profile %s: %w", profile.Name, err)
		}
	}
	if err := clearNetworkFromDefaultProfile(projectServer, plan.PrivateNetwork); err != nil {
		return fmt.Errorf("clear network from default profile: %w", err)
	}
	d.log("delete private network " + plan.PrivateNetwork)
	if err := ignoreNotFound(projectServer.DeleteNetwork(plan.PrivateNetwork)); err != nil {
		return fmt.Errorf("delete private network %s: %w", plan.PrivateNetwork, err)
	}
	if plan.PurgeDurableState {
		for _, volume := range plan.DurableVolumes {
			d.log("delete durable volume " + volume)
			if err := ignoreNotFound(projectServer.DeleteStoragePoolVolume(plan.StoragePool, "custom", volume)); err != nil {
				return fmt.Errorf("delete durable volume %s: %w", volume, err)
			}
		}
		images, err := projectServer.GetImages()
		if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
			return fmt.Errorf("list images in project %s: %w", plan.IncusProject, err)
		}
		for _, image := range images {
			d.log("delete project image " + image.Fingerprint[:8])
			op, err := projectServer.DeleteImage(image.Fingerprint)
			if err != nil {
				return fmt.Errorf("delete image %s: %w", image.Fingerprint[:8], err)
			}
			if err := op.Wait(); err != nil {
				return fmt.Errorf("wait for image %s delete: %w", image.Fingerprint[:8], err)
			}
		}
		d.log("delete Incus project " + plan.IncusProject)
		if err := ignoreNotFound(server.DeleteProject(plan.IncusProject)); err != nil {
			return fmt.Errorf("delete Incus project %s: %w", plan.IncusProject, err)
		}
		d.log("delete storage pool " + plan.StoragePool)
		if err := ignoreNotFound(server.DeleteStoragePool(plan.StoragePool)); err != nil {
			return fmt.Errorf("delete storage pool %s: %w", plan.StoragePool, err)
		}
	}
	d.log("done")
	return nil
}

func (d TenantDeleter) deleteProjectCompletely(server TenantDeleteServer, projectName string) error {
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
	images, err := projectServer.GetImages()
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("list images in project %s: %w", projectName, err)
	}
	for _, image := range images {
		d.log("delete image " + image.Fingerprint[:8] + " in " + projectName)
		op, err := projectServer.DeleteImage(image.Fingerprint)
		if err != nil {
			return fmt.Errorf("delete image %s in %s: %w", image.Fingerprint[:8], projectName, err)
		}
		if err := op.Wait(); err != nil {
			return fmt.Errorf("wait for image %s delete in %s: %w", image.Fingerprint[:8], projectName, err)
		}
	}
	profiles, err := projectServer.GetProfiles()
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("list profiles in project %s: %w", projectName, err)
	}
	for _, profile := range profiles {
		if profile.Name == "default" {
			continue
		}
		d.log("delete profile " + profile.Name + " in " + projectName)
		if err := ignoreNotFound(projectServer.DeleteProfile(profile.Name)); err != nil {
			return fmt.Errorf("delete profile %s in %s: %w", profile.Name, projectName, err)
		}
	}
	d.log("delete Incus project " + projectName)
	return ignoreNotFound(server.DeleteProject(projectName))
}

func deleteInstance(server TenantDeleteResourceServer, name string) error {
	instance, _, err := server.GetInstance(name)
	if err != nil {
		return ignoreNotFound(err)
	}
	if instance.IsActive() {
		op, err := server.UpdateInstanceState(name, api.InstanceStatePut{Action: "stop", Timeout: -1, Force: true}, "")
		if err != nil {
			return fmt.Errorf("stop instance %s: %w", name, err)
		}
		if err := op.Wait(); err != nil {
			return fmt.Errorf("wait for instance %s stop: %w", name, err)
		}
	}
	op, err := server.DeleteInstance(name)
	if err != nil {
		return fmt.Errorf("delete instance %s: %w", name, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for instance %s delete: %w", name, err)
	}
	return nil
}

// clearNetworkFromDefaultProfile removes any NIC devices referencing networkName
// from the default profile so Incus will allow deleting the network.
func clearNetworkFromDefaultProfile(server TenantDeleteResourceServer, networkName string) error {
	profile, etag, err := server.GetProfile("default")
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	changed := false
	for key, device := range profile.Devices {
		if device["type"] == "nic" && device["network"] == networkName {
			delete(profile.Devices, key)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return server.UpdateProfile("default", profile.Writable(), etag)
}

func ignoreNotFound(err error) error {
	if err != nil && api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil
	}
	return err
}

type sdkDeleteServer struct {
	inner incus.InstanceServer
}

func (s sdkDeleteServer) GetProjects() ([]api.Project, error) { return s.inner.GetProjects() }
func (s sdkDeleteServer) DeleteProject(name string) error     { return s.inner.DeleteProject(name) }
func (s sdkDeleteServer) DeleteStoragePool(name string) error {
	return s.inner.DeleteStoragePool(name)
}
func (s sdkDeleteServer) UseProject(name string) TenantDeleteResourceServer {
	return sdkDeleteResourceServer{inner: s.inner.UseProject(name)}
}

type sdkDeleteResourceServer struct {
	inner incus.InstanceServer
}

func (s sdkDeleteResourceServer) GetInstances(instanceType api.InstanceType) ([]api.Instance, error) {
	return s.inner.GetInstances(instanceType)
}
func (s sdkDeleteResourceServer) GetInstance(name string) (*api.Instance, string, error) {
	return s.inner.GetInstance(name)
}
func (s sdkDeleteResourceServer) UpdateInstanceState(name string, state api.InstanceStatePut, etag string) (incus.Operation, error) {
	return s.inner.UpdateInstanceState(name, state, etag)
}
func (s sdkDeleteResourceServer) DeleteInstance(name string) (incus.Operation, error) {
	return s.inner.DeleteInstance(name)
}
func (s sdkDeleteResourceServer) DeleteNetwork(name string) error { return s.inner.DeleteNetwork(name) }
func (s sdkDeleteResourceServer) DeleteStoragePoolVolume(pool string, volType string, name string) error {
	return s.inner.DeleteStoragePoolVolume(pool, volType, name)
}
func (s sdkDeleteResourceServer) GetImages() ([]api.Image, error) {
	return s.inner.GetImages()
}
func (s sdkDeleteResourceServer) DeleteImage(fingerprint string) (incus.Operation, error) {
	return s.inner.DeleteImage(fingerprint)
}
func (s sdkDeleteResourceServer) GetProfiles() ([]api.Profile, error) {
	return s.inner.GetProfiles()
}
func (s sdkDeleteResourceServer) GetProfile(name string) (*api.Profile, string, error) {
	return s.inner.GetProfile(name)
}
func (s sdkDeleteResourceServer) UpdateProfile(name string, profile api.ProfilePut, ETag string) error {
	return s.inner.UpdateProfile(name, profile, ETag)
}
func (s sdkDeleteResourceServer) DeleteProfile(name string) error {
	return s.inner.DeleteProfile(name)
}
