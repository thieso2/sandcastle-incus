package incusx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/infra"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

type InfrastructureCreator struct {
	Remote     string
	ConfigPath string
	Server     TenantCreateServer
	Log        func(string)
}

type InfrastructureDeleter struct {
	Remote     string
	ConfigPath string
	Server     TenantDeleteServer
	Log        func(string)
}

func NewInfrastructureCreator(remote string) InfrastructureCreator {
	return InfrastructureCreator{Remote: remote}
}

func (c InfrastructureCreator) WithVerbose(enabled bool, w io.Writer) InfrastructureCreator {
	if enabled {
		c.Log = func(msg string) { fmt.Fprint(w, msg) }
	}
	return c
}

func (c InfrastructureCreator) runCommand(label string, fn func() error) error {
	if c.Log == nil {
		return fn()
	}
	start := time.Now()
	c.Log(label + " ...")
	if err := fn(); err != nil {
		c.Log(fmt.Sprintf(" failed (%s)\n", formatVerboseDuration(time.Since(start))))
		return err
	}
	c.Log(fmt.Sprintf(" done (%s)\n", formatVerboseDuration(time.Since(start))))
	return nil
}

func NewInfrastructureDeleter(remote string) InfrastructureDeleter {
	return InfrastructureDeleter{Remote: remote}
}

func (d InfrastructureDeleter) WithVerbose(enabled bool, w io.Writer) InfrastructureDeleter {
	if enabled {
		d.Log = func(msg string) { fmt.Fprint(w, msg) }
	}
	return d
}

func (d InfrastructureDeleter) runCommand(label string, fn func() error) error {
	if d.Log == nil {
		return fn()
	}
	start := time.Now()
	d.Log(label + " ...")
	if err := fn(); err != nil {
		d.Log(fmt.Sprintf(" failed (%s)\n", formatVerboseDuration(time.Since(start))))
		return err
	}
	d.Log(fmt.Sprintf(" done (%s)\n", formatVerboseDuration(time.Since(start))))
	return nil
}

func formatVerboseDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return fmt.Sprintf("%dus", duration.Microseconds())
	}
	return duration.Round(time.Millisecond).String()
}

func (c InfrastructureCreator) CreateInfrastructure(ctx context.Context, plan infra.CreatePlan) error {
	server := c.Server
	remote := c.Remote
	if server == nil {
		loaded, err := cliconfig.LoadConfig(c.ConfigPath)
		if err != nil {
			return fmt.Errorf("load Incus config: %w", err)
		}
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkTenantCreateServer{inner: instanceServer}
	}
	if err := c.runCommand(fmt.Sprintf("incus project create/update %s:%s", remote, plan.Project), func() error {
		return ensureInfrastructureProject(server, plan)
	}); err != nil {
		return err
	}
	staticNetwork, err := infrastructureStaticNetwork(server)
	if err != nil {
		return err
	}
	plan = infra.ApplyStaticNetwork(plan, staticNetwork)
	projectServer := server.UseProject(plan.Project)
	for _, instance := range plan.Instances {
		label := fmt.Sprintf("incus launch/update %s %s:%s --project %s", instance.ImageAlias, remote, instance.Name, plan.Project)
		if err := c.runCommand(label, func() error { return ensureInfrastructureInstance(projectServer, instance) }); err != nil {
			return err
		}
	}
	if err := c.ensureInfrastructureRuntimeFiles(projectServer, remote, plan); err != nil {
		return err
	}
	if err := c.ensureInfrastructureRuntimeBinaries(projectServer, remote, plan); err != nil {
		return err
	}
	if err := c.runInfrastructureRuntimeCommands(projectServer, remote, plan); err != nil {
		return err
	}
	return nil
}

func infrastructureStaticNetwork(server TenantCreateServer) (infra.StaticNetwork, error) {
	defaultProject := server.UseProject("default")
	network, _, err := defaultProject.GetNetwork(infra.InfrastructureNetworkName)
	if err != nil {
		return infra.StaticNetwork{}, fmt.Errorf("get infrastructure network %s: %w", infra.InfrastructureNetworkName, err)
	}
	ipv4Address := strings.TrimSpace(network.Config["ipv4.address"])
	if ipv4Address == "" || ipv4Address == "none" {
		return infra.StaticNetwork{}, fmt.Errorf("infrastructure network %s has no IPv4 address", infra.InfrastructureNetworkName)
	}
	prefix, err := netip.ParsePrefix(ipv4Address)
	if err != nil {
		return infra.StaticNetwork{}, fmt.Errorf("parse infrastructure network %s IPv4 address %q: %w", infra.InfrastructureNetworkName, ipv4Address, err)
	}
	gateway := prefix.Addr()
	if !gateway.Is4() {
		return infra.StaticNetwork{}, fmt.Errorf("infrastructure network %s IPv4 address %q is not IPv4", infra.InfrastructureNetworkName, ipv4Address)
	}
	addresses, err := infrastructureStaticAddresses(prefix)
	if err != nil {
		return infra.StaticNetwork{}, err
	}
	return infra.StaticNetwork{
		Gateway:      gateway.String(),
		PrefixLength: prefix.Bits(),
		Addresses:    addresses,
	}, nil
}

func infrastructureStaticAddresses(prefix netip.Prefix) (map[string]string, error) {
	gateway4 := prefix.Addr().As4()
	candidates := map[string]byte{
		route.InfrastructureCaddyName: 20,
		infra.RouteBrokerName:         21,
		infra.AuthAppName:             22,
	}
	addresses := make(map[string]string, len(candidates))
	for instance, lastOctet := range candidates {
		address := netip.AddrFrom4([4]byte{gateway4[0], gateway4[1], gateway4[2], lastOctet})
		if !prefix.Contains(address) || address == prefix.Addr() {
			return nil, fmt.Errorf("derived infrastructure address %s for %s is outside %s", address, instance, prefix)
		}
		addresses[instance] = address.String()
	}
	return addresses, nil
}

func ensureInfrastructureProject(server TenantCreateServer, plan infra.CreatePlan) error {
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

func ensureInfrastructureInstance(server TenantResourceServer, instance infra.InstancePlan) error {
	existing, etag, err := server.GetInstance(instance.Name)
	if err == nil {
		if err := updateInfrastructureInstance(server, existing, etag, instance); err != nil {
			return err
		}
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

func updateInfrastructureInstance(server TenantResourceServer, existing *api.Instance, etag string, instance infra.InstancePlan) error {
	config := mergeConfig(map[string]string(existing.Config), instance.Config)
	op, err := server.UpdateInstance(instance.Name, api.InstancePut{
		Description: "Sandcastle infrastructure " + instance.Role,
		Config:      api.ConfigMap(config),
		Devices:     infrastructureDevicesMap(instance.Devices),
		Profiles:    []string{},
	}, etag)
	if err != nil {
		return fmt.Errorf("update infrastructure instance %s: %w", instance.Name, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for infrastructure instance %s update: %w", instance.Name, err)
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

func ensureInfrastructureRuntimeFiles(server TenantResourceServer, plan infra.CreatePlan) error {
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

func (c InfrastructureCreator) ensureInfrastructureRuntimeFiles(server TenantResourceServer, remote string, plan infra.CreatePlan) error {
	for _, directory := range plan.RuntimeDirectories {
		label := fmt.Sprintf("incus file mkdir %s:%s%s --project %s", remote, directory.Instance, directory.Path, plan.Project)
		err := c.runCommand(label, func() error {
			err := server.CreateInstanceFile(directory.Instance, directory.Path, incus.InstanceFileArgs{
				Type: "directory",
				Mode: directory.Mode,
			})
			if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
				return fmt.Errorf("create infrastructure runtime directory %s:%s: %w", directory.Instance, directory.Path, err)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	for _, file := range plan.RuntimeFiles {
		label := fmt.Sprintf("incus file push - %s:%s%s --project %s", remote, file.Instance, file.Path, plan.Project)
		err := c.runCommand(label, func() error {
			err := server.CreateInstanceFile(file.Instance, file.Path, incus.InstanceFileArgs{
				Content:   strings.NewReader(file.Content),
				Type:      "file",
				Mode:      file.Mode,
				WriteMode: "overwrite",
			})
			if err != nil {
				return fmt.Errorf("write infrastructure runtime file %s:%s: %w", file.Instance, file.Path, err)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func ensureInfrastructureRuntimeBinaries(server TenantResourceServer, plan infra.CreatePlan) error {
	for _, binary := range plan.RuntimeBinaries {
		data, err := os.ReadFile(binary.SourcePath)
		if err != nil {
			return fmt.Errorf("read infrastructure runtime binary %s: %w", binary.SourcePath, err)
		}
		err = server.CreateInstanceFile(binary.Instance, binary.TargetPath, incus.InstanceFileArgs{
			Content:   bytes.NewReader(data),
			Type:      "file",
			Mode:      binary.Mode,
			WriteMode: "overwrite",
		})
		if err != nil {
			return fmt.Errorf("write infrastructure runtime binary %s:%s: %w", binary.Instance, binary.TargetPath, err)
		}
	}
	return nil
}

func (c InfrastructureCreator) ensureInfrastructureRuntimeBinaries(server TenantResourceServer, remote string, plan infra.CreatePlan) error {
	for _, binary := range plan.RuntimeBinaries {
		label := fmt.Sprintf("incus file push %s %s:%s%s --project %s", binary.SourcePath, remote, binary.Instance, binary.TargetPath, plan.Project)
		err := c.runCommand(label, func() error {
			data, err := os.ReadFile(binary.SourcePath)
			if err != nil {
				return fmt.Errorf("read infrastructure runtime binary %s: %w", binary.SourcePath, err)
			}
			err = server.CreateInstanceFile(binary.Instance, binary.TargetPath, incus.InstanceFileArgs{
				Content:   bytes.NewReader(data),
				Type:      "file",
				Mode:      binary.Mode,
				WriteMode: "overwrite",
			})
			if err != nil {
				return fmt.Errorf("write infrastructure runtime binary %s:%s: %w", binary.Instance, binary.TargetPath, err)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func runInfrastructureRuntimeCommands(server TenantResourceServer, plan infra.CreatePlan) error {
	for _, command := range plan.RuntimeCommands {
		var stderr bytes.Buffer
		dataDone := make(chan bool)
		op, err := server.ExecInstance(command.Instance, api.InstanceExecPost{
			Command:   command.Command,
			WaitForWS: true,
		}, &incus.InstanceExecArgs{
			Stdin:    strings.NewReader(""),
			Stderr:   &stderr,
			DataDone: dataDone,
		})
		if err != nil {
			return fmt.Errorf("%s: %w", command.Description, err)
		}
		if err := op.Wait(); err != nil {
			return fmt.Errorf("wait for %s: %w (stderr: %s)", command.Description, err, stderr.String())
		}
		<-dataDone
	}
	return nil
}

func (c InfrastructureCreator) runInfrastructureRuntimeCommands(server TenantResourceServer, remote string, plan infra.CreatePlan) error {
	for _, command := range plan.RuntimeCommands {
		label := fmt.Sprintf("incus exec %s:%s --project %s -- %s", remote, command.Instance, plan.Project, strings.Join(command.Command, " "))
		err := c.runCommand(label, func() error {
			var stderr bytes.Buffer
			dataDone := make(chan bool)
			op, err := server.ExecInstance(command.Instance, api.InstanceExecPost{
				Command:   command.Command,
				WaitForWS: true,
			}, &incus.InstanceExecArgs{
				Stdin:    strings.NewReader(""),
				Stderr:   &stderr,
				DataDone: dataDone,
			})
			if err != nil {
				return fmt.Errorf("%s: %w", command.Description, err)
			}
			if err := op.Wait(); err != nil {
				return fmt.Errorf("wait for %s: %w (stderr: %s)", command.Description, err, stderr.String())
			}
			<-dataDone
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (d InfrastructureDeleter) DeleteInfrastructure(ctx context.Context, plan infra.DeletePlan) error {
	server := d.Server
	remote := d.Remote
	if server == nil {
		loaded, err := cliconfig.LoadConfig(d.ConfigPath)
		if err != nil {
			return fmt.Errorf("load Incus config: %w", err)
		}
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
		label := fmt.Sprintf("incus delete %s:%s --project %s --force", remote, name, plan.Project)
		if err := d.runCommand(label, func() error { return deleteInstance(projectServer, name) }); err != nil {
			return err
		}
	}
	label := fmt.Sprintf("incus project delete %s:%s", remote, plan.Project)
	if err := d.runCommand(label, func() error { return ignoreNotFound(server.DeleteProject(plan.Project)) }); err != nil {
		return fmt.Errorf("delete infrastructure project %s: %w", plan.Project, err)
	}
	return nil
}
