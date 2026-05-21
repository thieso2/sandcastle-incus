package incusx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	sandbox "github.com/thieso2/sandcastle-incus/internal/machine"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type SandboxCreateServer interface {
	UseProject(name string) SandboxResourceServer
}

type SandboxResourceServer interface {
	GetInstance(name string) (*api.Instance, string, error)
	CreateInstance(instance api.InstancesPost) (incus.Operation, error)
	UpdateInstanceState(name string, state api.InstanceStatePut, ETag string) (incus.Operation, error)
	CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error
	CreateStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error
	GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error)
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}

type SandboxCreator struct {
	Remote     string
	ConfigPath string
	Server     SandboxCreateServer
	Log        func(string)
}

func NewSandboxCreator(remote string) SandboxCreator {
	return SandboxCreator{Remote: remote}
}

func (c SandboxCreator) WithVerbose(enabled bool, w io.Writer) SandboxCreator {
	if enabled {
		c.Log = func(msg string) { fmt.Fprintln(w, "[sandbox-create] "+msg) }
	}
	return c
}

func (c SandboxCreator) log(msg string) {
	if c.Log != nil {
		c.Log(msg)
	}
}

func (c SandboxCreator) CreateMachine(ctx context.Context, plan sandbox.CreatePlan) error {
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
		c.log("connect to Incus remote " + remote)
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkSandboxServer{inner: instanceServer}
	}
	c.log("use project " + plan.Tenant.IncusName)
	projectServer := server.UseProject(plan.Tenant.IncusName)
	c.log("get instance " + plan.InstanceName)
	instance, _, err := projectServer.GetInstance(plan.InstanceName)
	if err == nil {
		if plan.StartsByDefault && !instance.IsActive() {
			c.log("start instance " + plan.InstanceName)
			op, err := projectServer.UpdateInstanceState(plan.InstanceName, api.InstanceStatePut{Action: "start", Timeout: -1}, "")
			if err != nil {
				return fmt.Errorf("start sandbox %s: %w", plan.InstanceName, err)
			}
			if err := op.Wait(); err != nil {
				return err
			}
		}
		c.log("ensure sandbox files for " + plan.InstanceName)
		return ensureSandboxFiles(projectServer, plan)
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get sandbox %s: %w", plan.InstanceName, err)
	}
	if err := ensureSandboxStorageDirs(projectServer, plan); err != nil {
		return err
	}
	c.log("create instance " + plan.InstanceName + " (image: " + plan.ImageAlias + ")")
	op, err := projectServer.CreateInstance(sandboxRequest(plan))
	if err != nil {
		return fmt.Errorf("create sandbox %s: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return err
	}
	c.log("ensure sandbox files for " + plan.InstanceName)
	return ensureSandboxFiles(projectServer, plan)
}

func ensureSandboxStorageDirs(server SandboxResourceServer, plan sandbox.CreatePlan) error {
	for _, volumeDir := range []struct {
		volume string
		path   string
	}{
		{volume: project.HomeVolumeName, path: plan.HomeDir},
		{volume: project.WorkspaceVolumeName, path: plan.WorkspaceDir},
	} {
		if volumeDir.path == "" || volumeDir.path == "." {
			continue
		}
		err := server.CreateStorageVolumeFile(plan.StoragePool, "custom", volumeDir.volume, volumeDir.path, incus.InstanceFileArgs{
			Type: "directory",
			UID:  int64(sandbox.DefaultLinuxUID),
			GID:  int64(sandbox.DefaultLinuxGID),
			Mode: 0o755,
		})
		if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
			return fmt.Errorf("create sandbox storage directory %s/%s: %w", volumeDir.volume, volumeDir.path, err)
		}
	}
	return nil
}

func ensureSandboxFiles(server SandboxResourceServer, plan sandbox.CreatePlan) error {
	if err := bootstrapSandboxUser(server, plan); err != nil {
		return err
	}
	if plan.Tenant.DNSAddress != "" {
		if err := writeSandboxResolvConf(server, plan.InstanceName, plan.Tenant.DNSAddress); err != nil {
			return fmt.Errorf("write sandbox resolv.conf: %w", err)
		}
	}
	certificateFiles := plan.CertificateFiles
	if len(certificateFiles) == 0 {
		var err error
		certificateFiles, err = issueSandboxCertificateFilesFromProjectCA(server, plan)
		if err != nil {
			return err
		}
	}
	for _, directory := range []string{
		"/etc/caddy",
		"/etc/caddy/certs",
		"/etc/systemd/system/caddy.service.d",
	} {
		err := server.CreateInstanceFile(plan.InstanceName, directory, incus.InstanceFileArgs{
			Type: "directory",
			Mode: 0o755,
		})
		if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
			return fmt.Errorf("create sandbox config directory %s: %w", directory, err)
		}
	}
	if err := server.CreateInstanceFile(plan.InstanceName, "/etc/systemd/system/caddy.service.d/sandbox.conf", incus.InstanceFileArgs{
		Content:   strings.NewReader("[Service]\nUser=root\nGroup=root\n"),
		Type:      "file",
		Mode:      0o644,
		WriteMode: "overwrite",
	}); err != nil {
		return fmt.Errorf("write Caddy service override: %w", err)
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
	ipWithPrefix, gateway, err := sandboxNetworkParams(plan)
	if err != nil {
		return err
	}
	return restartSandboxCaddy(server, plan.InstanceName, ipWithPrefix, gateway)
}

func writeSandboxResolvConf(server SandboxResourceServer, instanceName string, dnsAddress string) error {
	content := "nameserver " + dnsAddress + "\n"
	err := server.CreateInstanceFile(instanceName, "/etc/resolv.conf", incus.InstanceFileArgs{
		Content:   strings.NewReader(content),
		Type:      "file",
		Mode:      0o644,
		WriteMode: "overwrite",
	})
	if err == nil || !api.StatusErrorCheck(err, http.StatusNotFound) {
		return err
	}
	dataDone := make(chan bool)
	op, err := server.ExecInstance(instanceName, api.InstanceExecPost{
		Command:   []string{"/bin/sh", "-c", "rm -f /etc/resolv.conf && cat > /etc/resolv.conf && chmod 0644 /etc/resolv.conf"},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(content),
		DataDone: dataDone,
	})
	if err != nil {
		return err
	}
	if err := op.Wait(); err != nil {
		return err
	}
	<-dataDone
	return nil
}

func sandboxNetworkParams(plan sandbox.CreatePlan) (string, string, error) {
	if plan.PrivateIP == "" || plan.Tenant.PrivateCIDR == "" {
		return "", "", fmt.Errorf("sandbox plan missing private IP or CIDR")
	}
	prefix, err := netip.ParsePrefix(plan.Tenant.PrivateCIDR)
	if err != nil {
		return "", "", fmt.Errorf("parse sandbox CIDR %s: %w", plan.Tenant.PrivateCIDR, err)
	}
	ipWithPrefix := plan.PrivateIP + fmt.Sprintf("/%d", prefix.Bits())
	base := prefix.Masked().Addr().As4()
	base[3] = 1
	gateway := netip.AddrFrom4(base).String()
	return ipWithPrefix, gateway, nil
}

func bootstrapSandboxUser(server SandboxResourceServer, plan sandbox.CreatePlan) error {
	dataDone := make(chan bool)
	op, err := server.ExecInstance(plan.InstanceName, api.InstanceExecPost{
		Command: []string{"/usr/local/bin/sandcastle-bootstrap"},
		Environment: map[string]string{
			"SANDCASTLE_USER":           plan.LinuxUser,
			"SANDCASTLE_UID":            fmt.Sprintf("%d", sandbox.DefaultLinuxUID),
			"SANDCASTLE_GID":            fmt.Sprintf("%d", sandbox.DefaultLinuxGID),
			"SANDCASTLE_SSH_PUBLIC_KEY": plan.SSHPublicKey,
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

func restartSandboxCaddy(server sandboxCaddyRestarter, instanceName string, privateIPWithPrefix string, gateway string) error {
	var cmds []string
	if privateIPWithPrefix != "" && gateway != "" {
		cmds = append(cmds,
			"/usr/sbin/ip link set eth0 up",
			"/usr/sbin/ip addr add "+privateIPWithPrefix+" dev eth0 2>/dev/null || true",
			"/usr/sbin/ip route add default via "+gateway+" 2>/dev/null || true",
		)
	}
	cmds = append(cmds,
		"install -d /etc/caddy",
		"systemctl daemon-reload",
		"systemctl restart caddy",
		"for i in $(seq 1 50); do systemctl is-active caddy >/dev/null 2>&1 && exit 0; sleep 0.1; done",
		"systemctl is-active caddy",
	)
	var stderr bytes.Buffer
	dataDone := make(chan bool)
	op, err := server.ExecInstance(instanceName, api.InstanceExecPost{
		Command:   []string{"/bin/sh", "-c", strings.Join(cmds, " && ")},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		Stderr:   &stderr,
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("restart sandbox Caddy in %s: %w", instanceName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for sandbox Caddy restart in %s (stderr: %s): %w", instanceName, stderr.String(), err)
	}
	<-dataDone
	return nil
}

func issueSandboxCertificateFilesFromProjectCA(server SandboxResourceServer, plan sandbox.CreatePlan) ([]sandbox.File, error) {
	caCertPEM, err := readProjectCAFile(server, plan, project.TenantCACertPath)
	if err != nil {
		return nil, fmt.Errorf("read project CA certificate: %w", err)
	}
	caKeyPEM, err := readProjectCAFile(server, plan, project.TenantCAKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read project CA private key: %w", err)
	}
	files, err := sandbox.IssueCertificateFiles(plan.Name, plan.Project, plan.Tenant.DNSSuffix, caCertPEM, caKeyPEM)
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
	if plan.ContainerTools {
		config["security.nesting"] = "true"
	}
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
	return sdkSandboxResourceServer{inner: s.inner.UseProject(name), projectName: name}
}

type sdkSandboxResourceServer struct {
	inner       incus.InstanceServer
	projectName string
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

func (s sdkSandboxResourceServer) CreateStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error {
	return createStorageVolumeFile(s.inner, s.projectName, pool, volumeType, volumeName, filePath, args)
}

func (s sdkSandboxResourceServer) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	return getStorageVolumeFile(s.inner, s.projectName, pool, volumeType, volumeName, filePath)
}

func (s sdkSandboxResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	return s.inner.ExecInstance(instanceName, exec, args)
}
