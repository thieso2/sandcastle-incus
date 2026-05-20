package incusx

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/infra"
)

type InfrastructureCreator struct {
	Remote     string
	ConfigPath string
	Server     ProjectCreateServer
}

type InfrastructureDeleter struct {
	Remote     string
	ConfigPath string
	Server     ProjectDeleteServer
}

func NewInfrastructureCreator(remote string) InfrastructureCreator {
	return InfrastructureCreator{Remote: remote}
}

func NewInfrastructureDeleter(remote string) InfrastructureDeleter {
	return InfrastructureDeleter{Remote: remote}
}

func (c InfrastructureCreator) CreateInfrastructure(ctx context.Context, plan infra.CreatePlan) error {
	server := c.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(c.ConfigPath)
		if err != nil {
			return fmt.Errorf("load Incus config: %w", err)
		}
		remote := c.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkProjectServer{inner: instanceServer}
	}
	if err := ensureInfrastructureProject(server, plan); err != nil {
		return err
	}
	projectServer := server.UseProject(plan.Project)
	for _, instance := range plan.Instances {
		if err := ensureInfrastructureInstance(projectServer, instance); err != nil {
			return err
		}
	}
	if err := ensureInfrastructureRuntimeFiles(projectServer, plan); err != nil {
		return err
	}
	if err := runInfrastructureRuntimeCommands(projectServer, plan); err != nil {
		return err
	}
	return nil
}

func ensureInfrastructureProject(server ProjectCreateServer, plan infra.CreatePlan) error {
	existing, etag, err := server.GetProject(plan.Project)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return server.CreateProject(api.ProjectsPost{
				Name: plan.Project,
				ProjectPut: api.ProjectPut{
					Description: "Sandcastle infrastructure",
					Config:      api.ConfigMap(plan.ProjectMetadataConfig),
				},
			})
		}
		return fmt.Errorf("get infrastructure project %s: %w", plan.Project, err)
	}
	config := mergeConfig(map[string]string(existing.Config), plan.ProjectMetadataConfig)
	if err := server.UpdateProject(plan.Project, api.ProjectPut{
		Description: existing.Description,
		Config:      api.ConfigMap(config),
	}, etag); err != nil {
		return fmt.Errorf("update infrastructure project %s metadata: %w", plan.Project, err)
	}
	return nil
}

func ensureInfrastructureInstance(server ProjectResourceServer, instance infra.InstancePlan) error {
	existing, _, err := server.GetInstance(instance.Name)
	if err == nil {
		if instance.Start && !existing.IsActive() {
			op, err := server.UpdateInstanceState(instance.Name, api.InstanceStatePut{
				Action:  "start",
				Timeout: -1,
			}, "")
			if err != nil {
				return fmt.Errorf("start infrastructure instance %s: %w", instance.Name, err)
			}
			if err := op.Wait(); err != nil {
				return fmt.Errorf("wait for infrastructure instance %s start: %w", instance.Name, err)
			}
		}
		return nil
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get infrastructure instance %s: %w", instance.Name, err)
	}
	op, err := server.CreateInstance(infrastructureInstanceRequest(instance))
	if err != nil {
		return fmt.Errorf("create infrastructure instance %s: %w", instance.Name, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for infrastructure instance %s create: %w", instance.Name, err)
	}
	return nil
}

func infrastructureInstanceRequest(instance infra.InstancePlan) api.InstancesPost {
	return api.InstancesPost{
		Name:  instance.Name,
		Type:  "container",
		Start: instance.Start,
		Source: api.InstanceSource{
			Type:  "image",
			Alias: instance.ImageAlias,
		},
		InstancePut: api.InstancePut{
			Description: "Sandcastle infrastructure " + instance.Role,
			Config:      api.ConfigMap(instance.Config),
			Devices:     infrastructureDevicesMap(instance.Devices),
			Profiles:    []string{},
		},
	}
}

func infrastructureDevicesMap(devices map[string]infra.Device) api.DevicesMap {
	output := make(api.DevicesMap, len(devices))
	for name, device := range devices {
		output[name] = map[string]string(device)
	}
	return output
}

func ensureInfrastructureRuntimeFiles(server ProjectResourceServer, plan infra.CreatePlan) error {
	for _, directory := range plan.RuntimeDirectories {
		err := server.CreateInstanceFile(directory.Instance, directory.Path, incus.InstanceFileArgs{
			Type: "directory",
			Mode: directory.Mode,
		})
		if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
			return fmt.Errorf("create infrastructure runtime directory %s:%s: %w", directory.Instance, directory.Path, err)
		}
	}
	for _, file := range plan.RuntimeFiles {
		err := server.CreateInstanceFile(file.Instance, file.Path, incus.InstanceFileArgs{
			Content:   strings.NewReader(file.Content),
			Type:      "file",
			Mode:      file.Mode,
			WriteMode: "overwrite",
		})
		if err != nil {
			return fmt.Errorf("write infrastructure runtime file %s:%s: %w", file.Instance, file.Path, err)
		}
	}
	return nil
}

func runInfrastructureRuntimeCommands(server ProjectResourceServer, plan infra.CreatePlan) error {
	for _, command := range plan.RuntimeCommands {
		dataDone := make(chan bool)
		op, err := server.ExecInstance(command.Instance, api.InstanceExecPost{
			Command:   command.Command,
			WaitForWS: true,
		}, &incus.InstanceExecArgs{
			Stdin:    strings.NewReader(""),
			DataDone: dataDone,
		})
		if err != nil {
			return fmt.Errorf("%s: %w", command.Description, err)
		}
		if err := op.Wait(); err != nil {
			return fmt.Errorf("wait for %s: %w", command.Description, err)
		}
		<-dataDone
	}
	return nil
}

func (d InfrastructureDeleter) DeleteInfrastructure(ctx context.Context, plan infra.DeletePlan) error {
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
	projectServer := server.UseProject(plan.Project)
	for _, name := range plan.RuntimeInstances {
		if err := deleteInstance(projectServer, name); err != nil {
			return err
		}
	}
	if err := ignoreNotFound(server.DeleteProject(plan.Project)); err != nil {
		return fmt.Errorf("delete infrastructure project %s: %w", plan.Project, err)
	}
	return nil
}
