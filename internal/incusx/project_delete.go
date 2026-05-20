package incusx

import (
	"context"
	"fmt"
	"io"
	"net/http"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type ProjectDeleteServer interface {
	DeleteProject(name string) error
	UseProject(name string) ProjectDeleteResourceServer
}

type ProjectDeleteResourceServer interface {
	GetInstances(instanceType api.InstanceType) ([]api.Instance, error)
	GetInstance(name string) (*api.Instance, string, error)
	UpdateInstanceState(name string, state api.InstanceStatePut, ETag string) (incus.Operation, error)
	DeleteInstance(name string) (incus.Operation, error)
	DeleteNetwork(name string) error
	DeleteStoragePoolVolume(pool string, volType string, name string) error
}

type ProjectDeleter struct {
	Remote     string
	ConfigPath string
	Server     ProjectDeleteServer
	Log        func(string)
}

func NewProjectDeleter(remote string) ProjectDeleter {
	return ProjectDeleter{Remote: remote}
}

func (d ProjectDeleter) WithVerbose(enabled bool, w io.Writer) ProjectDeleter {
	if enabled {
		d.Log = func(msg string) { fmt.Fprintln(w, "[project-delete] "+msg) }
	}
	return d
}

func (d ProjectDeleter) log(msg string) {
	if d.Log != nil {
		d.Log(msg)
	}
}

func (d ProjectDeleter) DeleteProject(ctx context.Context, plan project.DeletePlan) error {
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
		d.log("delete Incus project " + plan.IncusProject)
		if err := ignoreNotFound(server.DeleteProject(plan.IncusProject)); err != nil {
			return fmt.Errorf("delete Incus project %s: %w", plan.IncusProject, err)
		}
	}
	d.log("done")
	return nil
}

func deleteInstance(server ProjectDeleteResourceServer, name string) error {
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

func ignoreNotFound(err error) error {
	if err != nil && api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil
	}
	return err
}

type sdkDeleteServer struct {
	inner incus.InstanceServer
}

func (s sdkDeleteServer) DeleteProject(name string) error { return s.inner.DeleteProject(name) }
func (s sdkDeleteServer) UseProject(name string) ProjectDeleteResourceServer {
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
