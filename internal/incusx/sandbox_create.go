package incusx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/project"
	"github.com/thieso2/sandcastle-incus/internal/sandbox"
)

type SandboxCreateServer interface {
	UseProject(name string) SandboxResourceServer
}

type SandboxResourceServer interface {
	GetInstance(name string) (*api.Instance, string, error)
	CreateInstance(instance api.InstancesPost) (incus.Operation, error)
	UpdateInstanceState(name string, state api.InstanceStatePut, ETag string) (incus.Operation, error)
	CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error
	GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error)
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}

type SandboxCreator struct {
	Remote     string
	ConfigPath string
	Server     SandboxCreateServer
}

func NewSandboxCreator(remote string) SandboxCreator {
	return SandboxCreator{Remote: remote}
}

func (c SandboxCreator) CreateSandbox(ctx context.Context, plan sandbox.CreatePlan) error {
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
		server = sdkSandboxServer{inner: instanceServer}
	}
	projectServer := server.UseProject(plan.Project.IncusName)
	instance, _, err := projectServer.GetInstance(plan.InstanceName)
	if err == nil {
		if plan.StartsByDefault && !instance.IsActive() {
			op, err := projectServer.UpdateInstanceState(plan.InstanceName, api.InstanceStatePut{Action: "start", Timeout: -1}, "")
			if err != nil {
				return fmt.Errorf("start sandbox %s: %w", plan.InstanceName, err)
			}
			if err := op.Wait(); err != nil {
				return err
			}
		}
		return ensureSandboxFiles(projectServer, plan)
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get sandbox %s: %w", plan.InstanceName, err)
	}
	op, err := projectServer.CreateInstance(sandboxRequest(plan))
	if err != nil {
		return fmt.Errorf("create sandbox %s: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return err
	}
	return ensureSandboxFiles(projectServer, plan)
}

func ensureSandboxFiles(server SandboxResourceServer, plan sandbox.CreatePlan) error {
	if err := bootstrapSandboxUser(server, plan); err != nil {
		return err
	}
	certificateFiles := plan.CertificateFiles
	if len(certificateFiles) == 0 {
		var err error
		certificateFiles, err = issueSandboxCertificateFilesFromProjectCA(server, plan)
		if err != nil {
			return err
		}
	}
	for _, directory := range []string{"/etc/caddy", "/etc/caddy/certs"} {
		err := server.CreateInstanceFile(plan.InstanceName, directory, incus.InstanceFileArgs{
			Type: "directory",
			Mode: 0o755,
		})
		if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
			return fmt.Errorf("create sandbox config directory %s: %w", directory, err)
		}
	}
	if err := server.CreateInstanceFile(plan.InstanceName, plan.CaddyFile.Path, incus.InstanceFileArgs{
		Content:   strings.NewReader(plan.CaddyFile.Content),
		Type:      "file",
		Mode:      plan.CaddyFile.Mode,
		WriteMode: "overwrite",
	}); err != nil {
		return fmt.Errorf("write sandbox Caddyfile %s: %w", plan.CaddyFile.Path, err)
	}
	for _, file := range certificateFiles {
		if err := server.CreateInstanceFile(plan.InstanceName, file.Path, incus.InstanceFileArgs{
			Content:   strings.NewReader(string(file.Content)),
			Type:      "file",
			Mode:      file.Mode,
			WriteMode: "overwrite",
		}); err != nil {
			return fmt.Errorf("write sandbox certificate file %s: %w", file.Path, err)
		}
	}
	return restartSandboxCaddy(server, plan.InstanceName)
}

func bootstrapSandboxUser(server SandboxResourceServer, plan sandbox.CreatePlan) error {
	dataDone := make(chan bool)
	op, err := server.ExecInstance(plan.InstanceName, api.InstanceExecPost{
		Command: []string{"/usr/local/bin/sandcastle-bootstrap"},
		Environment: map[string]string{
			"SANDCASTLE_USER": plan.LinuxUser,
			"SANDCASTLE_UID":  fmt.Sprintf("%d", sandbox.DefaultLinuxUID),
			"SANDCASTLE_GID":  fmt.Sprintf("%d", sandbox.DefaultLinuxGID),
		},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("bootstrap sandbox user %s in %s: %w", plan.LinuxUser, plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for sandbox user bootstrap in %s: %w", plan.InstanceName, err)
	}
	<-dataDone
	return nil
}

type sandboxCaddyRestarter interface {
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}

func restartSandboxCaddy(server sandboxCaddyRestarter, instanceName string) error {
	dataDone := make(chan bool)
	op, err := server.ExecInstance(instanceName, api.InstanceExecPost{
		Command: []string{"/bin/sh", "-lc", strings.Join([]string{
			"if pgrep -x caddy >/dev/null 2>&1; then caddy reload --config /etc/caddy/Caddyfile; else nohup caddy run --config /etc/caddy/Caddyfile >/var/log/caddy.log 2>&1 & fi",
			"for i in $(seq 1 50); do caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1 && exit 0; sleep 0.1; done",
			"pgrep -x caddy >/dev/null 2>&1",
		}, "; ")},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("restart sandbox Caddy in %s: %w", instanceName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for sandbox Caddy restart in %s: %w", instanceName, err)
	}
	<-dataDone
	return nil
}

func issueSandboxCertificateFilesFromProjectCA(server SandboxResourceServer, plan sandbox.CreatePlan) ([]sandbox.File, error) {
	caCertPEM, err := readProjectCAFile(server, plan, project.ProjectCACertPath)
	if err != nil {
		return nil, fmt.Errorf("read project CA certificate: %w", err)
	}
	caKeyPEM, err := readProjectCAFile(server, plan, project.ProjectCAKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read project CA private key: %w", err)
	}
	files, err := sandbox.IssueCertificateFiles(plan.Name, plan.Project.Domain, caCertPEM, caKeyPEM)
	if err != nil {
		return nil, err
	}
	return files, nil
}

func readProjectCAFile(server SandboxResourceServer, plan sandbox.CreatePlan, path string) ([]byte, error) {
	content, _, err := server.GetStorageVolumeFile(plan.StoragePool, "custom", plan.CAVolume, path)
	if err != nil {
		return nil, err
	}
	defer content.Close()
	return io.ReadAll(content)
}

func sandboxRequest(plan sandbox.CreatePlan) api.InstancesPost {
	config := map[string]string{}
	for key, value := range plan.MetadataConfig {
		config[key] = value
	}
	config["environment.SANDCASTLE_USER"] = plan.LinuxUser
	config["environment.USER"] = plan.LinuxUser
	config["environment.HOME"] = "/home/" + plan.LinuxUser
	return api.InstancesPost{
		Name:  plan.InstanceName,
		Type:  "container",
		Start: plan.StartsByDefault,
		Source: api.InstanceSource{
			Type:  "image",
			Alias: plan.ImageAlias,
		},
		InstancePut: api.InstancePut{
			Description: "Sandcastle sandbox " + plan.Reference,
			Config:      api.ConfigMap(config),
			Devices:     sandboxDevicesMap(plan.Devices),
			Profiles:    []string{},
		},
	}
}

func sandboxDevicesMap(devices map[string]sandbox.Device) api.DevicesMap {
	output := make(api.DevicesMap, len(devices))
	for name, device := range devices {
		output[name] = map[string]string(device)
	}
	return output
}

type sdkSandboxServer struct {
	inner incus.InstanceServer
}

func (s sdkSandboxServer) UseProject(name string) SandboxResourceServer {
	return sdkSandboxResourceServer{inner: s.inner.UseProject(name)}
}

type sdkSandboxResourceServer struct {
	inner incus.InstanceServer
}

func (s sdkSandboxResourceServer) GetInstance(name string) (*api.Instance, string, error) {
	return s.inner.GetInstance(name)
}

func (s sdkSandboxResourceServer) CreateInstance(instance api.InstancesPost) (incus.Operation, error) {
	return s.inner.CreateInstance(instance)
}

func (s sdkSandboxResourceServer) UpdateInstanceState(name string, state api.InstanceStatePut, etag string) (incus.Operation, error) {
	return s.inner.UpdateInstanceState(name, state, etag)
}

func (s sdkSandboxResourceServer) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
	return s.inner.CreateInstanceFile(instanceName, path, args)
}

func (s sdkSandboxResourceServer) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	return s.inner.GetStorageVolumeFile(pool, volumeType, volumeName, filePath)
}

func (s sdkSandboxResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	return s.inner.ExecInstance(instanceName, exec, args)
}
