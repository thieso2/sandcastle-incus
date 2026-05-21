package incusx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"path"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type MachineCreateServer interface {
	UseProject(name string) MachineResourceServer
}

type MachineResourceServer interface {
	GetInstance(name string) (*api.Instance, string, error)
	CreateInstance(instance api.InstancesPost) (incus.Operation, error)
	UpdateInstanceState(name string, state api.InstanceStatePut, ETag string) (incus.Operation, error)
	DeleteInstance(name string) (incus.Operation, error)
	CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error
	CreateStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error
	GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error)
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}

type MachineCreator struct {
	Remote     string
	ConfigPath string
	Server     MachineCreateServer
	Log        func(string)
}

func NewMachineCreator(remote string) MachineCreator {
	return MachineCreator{Remote: remote}
}

func (c MachineCreator) WithVerbose(enabled bool, w io.Writer) MachineCreator {
	if enabled {
		c.Log = func(msg string) { fmt.Fprintln(w, "[machine-create] "+msg) }
	}
	return c
}

func (c MachineCreator) log(msg string) {
	if c.Log != nil {
		c.Log(msg)
	}
}

func (c MachineCreator) CreateMachine(ctx context.Context, plan machine.CreatePlan) error {
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
		server = sdkMachineServer{inner: instanceServer}
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
				return fmt.Errorf("start machine %s: %w", plan.InstanceName, err)
			}
			if err := op.Wait(); err != nil {
				return err
			}
		}
		c.log("ensure machine files for " + plan.InstanceName)
		return ensureMachineFiles(projectServer, plan)
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get machine %s: %w", plan.InstanceName, err)
	}
	if err := ensureMachineStorageDirs(projectServer, plan); err != nil {
		return err
	}
	c.log("create instance " + plan.InstanceName + " (image: " + plan.ImageAlias + ")")
	op, err := projectServer.CreateInstance(machineRequest(plan))
	if err != nil {
		return fmt.Errorf("create machine %s: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return err
	}
	c.log("ensure machine files for " + plan.InstanceName)
	return ensureMachineFiles(projectServer, plan)
}

func ensureMachineStorageDirs(server MachineResourceServer, plan machine.CreatePlan) error {
	var helperDirs []machineStorageDir
	for _, volumeDir := range []machineStorageDir{
		{volume: tenant.HomeVolumeName, path: plan.HomeDir},
		{volume: tenant.WorkspaceVolumeName, path: plan.WorkspaceDir},
	} {
		if volumeDir.path == "" || volumeDir.path == "." {
			continue
		}
		err := server.CreateStorageVolumeFile(plan.StoragePool, "custom", volumeDir.volume, volumeDir.path, incus.InstanceFileArgs{
			Type: "directory",
			UID:  int64(machine.DefaultLinuxUID),
			GID:  int64(machine.DefaultLinuxGID),
			Mode: 0o755,
		})
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			helperDirs = append(helperDirs, volumeDir)
			continue
		}
		if err != nil && !api.StatusErrorCheck(err, http.StatusConflict) {
			return fmt.Errorf("create machine storage directory %s/%s: %w", volumeDir.volume, volumeDir.path, err)
		}
	}
	if len(helperDirs) > 0 {
		return ensureMachineStorageDirsWithHelper(server, plan, helperDirs)
	}
	return nil
}

type machineStorageDir struct {
	volume string
	path   string
}

func ensureMachineStorageDirsWithHelper(server MachineResourceServer, plan machine.CreatePlan, dirs []machineStorageDir) error {
	name := machineStorageHelperName(plan.InstanceName)
	_ = deleteMachineStorageHelper(server, name)
	helper := machineStorageHelperRequest(plan, name)
	op, err := server.CreateInstance(helper)
	if err != nil {
		return fmt.Errorf("create machine storage helper %s: %w", name, err)
	}
	if err := op.Wait(); err != nil {
		_ = deleteMachineStorageHelper(server, name)
		return fmt.Errorf("wait for machine storage helper %s: %w", name, err)
	}
	defer deleteMachineStorageHelper(server, name)

	var commands []string
	for _, dir := range dirs {
		target := "/mnt/" + storageHelperMountName(dir.volume) + "/" + path.Clean(dir.path)
		commands = append(commands, "install -d -o 1000 -g 1000 -m 0755 -- "+shellQuote(target))
	}
	dataDone := make(chan bool)
	op, err = server.ExecInstance(name, api.InstanceExecPost{
		Command:   []string{"/bin/sh", "-c", strings.Join(commands, " && ")},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("create machine storage directories with helper %s: %w", name, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for machine storage helper %s directory creation: %w", name, err)
	}
	<-dataDone
	return nil
}

func machineStorageHelperRequest(plan machine.CreatePlan, name string) api.InstancesPost {
	return api.InstancesPost{
		Name:  name,
		Type:  "container",
		Start: true,
		Source: api.InstanceSource{
			Type:  "image",
			Alias: plan.ImageAlias,
		},
		InstancePut: api.InstancePut{
			Devices: api.DevicesMap{
				"root": {
					"type": "disk",
					"pool": plan.StoragePool,
					"path": "/",
				},
				"home": {
					"type":   "disk",
					"pool":   plan.StoragePool,
					"source": tenant.HomeVolumeName,
					"path":   "/mnt/home",
				},
				"workspace": {
					"type":   "disk",
					"pool":   plan.StoragePool,
					"source": tenant.WorkspaceVolumeName,
					"path":   "/mnt/workspace",
				},
			},
		},
	}
}

func deleteMachineStorageHelper(server MachineResourceServer, name string) error {
	stopOp, stopErr := server.UpdateInstanceState(name, api.InstanceStatePut{Action: "stop", Timeout: -1, Force: true}, "")
	if stopErr == nil {
		_ = stopOp.Wait()
	}
	op, err := server.DeleteInstance(name)
	if api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil
	}
	return waitOperation(op, err, "delete machine storage helper "+name)
}

func machineStorageHelperName(instanceName string) string {
	sum := sha256.Sum256([]byte(instanceName))
	return fmt.Sprintf("sc-storage-init-%x", sum[:6])
}

func storageHelperMountName(volume string) string {
	switch volume {
	case tenant.HomeVolumeName:
		return "home"
	case tenant.WorkspaceVolumeName:
		return "workspace"
	default:
		return volume
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func ensureMachineFiles(server MachineResourceServer, plan machine.CreatePlan) error {
	if err := setMachineHostname(server, plan); err != nil {
		return err
	}
	if err := bootstrapMachineUser(server, plan); err != nil {
		return err
	}
	if err := ensureMachinePrompt(server, plan); err != nil {
		return err
	}
	if plan.Tenant.DNSAddress != "" {
		if err := writeMachineResolvConf(server, plan.InstanceName, plan.Tenant.DNSAddress); err != nil {
			return fmt.Errorf("write machine resolv.conf: %w", err)
		}
	}
	certificateFiles := plan.CertificateFiles
	if len(certificateFiles) == 0 {
		var err error
		certificateFiles, err = issueMachineCertificateFilesFromProjectCA(server, plan)
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
			return fmt.Errorf("create machine config directory %s: %w", directory, err)
		}
	}
	if err := server.CreateInstanceFile(plan.InstanceName, "/etc/systemd/system/caddy.service.d/machine.conf", incus.InstanceFileArgs{
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
		return fmt.Errorf("write machine Caddyfile %s: %w", plan.CaddyFile.Path, err)
	}
	for _, file := range certificateFiles {
		if err := server.CreateInstanceFile(plan.InstanceName, file.Path, incus.InstanceFileArgs{
			Content:   strings.NewReader(string(file.Content)),
			Type:      "file",
			Mode:      file.Mode,
			WriteMode: "overwrite",
		}); err != nil {
			return fmt.Errorf("write machine certificate file %s: %w", file.Path, err)
		}
	}
	ipWithPrefix, gateway, err := machineNetworkParams(plan)
	if err != nil {
		return err
	}
	return restartMachineCaddy(server, plan.InstanceName, ipWithPrefix, gateway)
}

func setMachineHostname(server MachineResourceServer, plan machine.CreatePlan) error {
	if strings.TrimSpace(plan.Hostname) == "" {
		return nil
	}
	if err := server.CreateInstanceFile(plan.InstanceName, "/etc/hostname", incus.InstanceFileArgs{
		Content:   strings.NewReader(plan.Hostname + "\n"),
		Type:      "file",
		Mode:      0o644,
		WriteMode: "overwrite",
	}); err != nil {
		return fmt.Errorf("write machine hostname file: %w", err)
	}
	dataDone := make(chan bool)
	op, err := server.ExecInstance(plan.InstanceName, api.InstanceExecPost{
		Command:   []string{"hostname", plan.Hostname},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("set machine hostname in %s: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for machine hostname in %s: %w", plan.InstanceName, err)
	}
	<-dataDone
	return nil
}

func writeMachineResolvConf(server MachineResourceServer, instanceName string, dnsAddress string) error {
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

func machineNetworkParams(plan machine.CreatePlan) (string, string, error) {
	if plan.PrivateIP == "" || plan.Tenant.PrivateCIDR == "" {
		return "", "", fmt.Errorf("machine plan missing private IP or CIDR")
	}
	prefix, err := netip.ParsePrefix(plan.Tenant.PrivateCIDR)
	if err != nil {
		return "", "", fmt.Errorf("parse machine CIDR %s: %w", plan.Tenant.PrivateCIDR, err)
	}
	ipWithPrefix := plan.PrivateIP + fmt.Sprintf("/%d", prefix.Bits())
	base := prefix.Masked().Addr().As4()
	base[3] = 1
	gateway := netip.AddrFrom4(base).String()
	return ipWithPrefix, gateway, nil
}

func bootstrapMachineUser(server MachineResourceServer, plan machine.CreatePlan) error {
	dataDone := make(chan bool)
	op, err := server.ExecInstance(plan.InstanceName, api.InstanceExecPost{
		Command: []string{"/usr/local/bin/sandcastle-bootstrap"},
		Environment: map[string]string{
			"SANDCASTLE_USER":           plan.LinuxUser,
			"SANDCASTLE_UID":            fmt.Sprintf("%d", machine.DefaultLinuxUID),
			"SANDCASTLE_GID":            fmt.Sprintf("%d", machine.DefaultLinuxGID),
			"SANDCASTLE_SSH_PUBLIC_KEY": plan.SSHPublicKey,
		},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("bootstrap machine user %s in %s: %w", plan.LinuxUser, plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for machine user bootstrap in %s: %w", plan.InstanceName, err)
	}
	<-dataDone
	return nil
}

func ensureMachinePrompt(server MachineResourceServer, plan machine.CreatePlan) error {
	script := `set -eu
user="${SANDCASTLE_USER:?}"
home="/home/${user}"
bashrc="${home}/.bashrc"
profile="${home}/.bash_profile"
install -d -o "${user}" -g "${user}" "${home}"
touch "${bashrc}" "${profile}"
chown "${user}:${user}" "${bashrc}" "${profile}"

prompt_marker="# sandcastle prompt: full hostname"
if ! grep -qF "${prompt_marker}" "${bashrc}"; then
  cat >>"${bashrc}" <<'EOF_PROMPT'

# sandcastle prompt: full hostname
if [[ $- == *i* ]]; then
  PS1='\u@\H:\w\$ '
fi
EOF_PROMPT
fi

profile_marker="# sandcastle bash profile: source bashrc"
if ! grep -qF "${profile_marker}" "${profile}"; then
  cat >>"${profile}" <<'EOF_PROFILE'

# sandcastle bash profile: source bashrc
if [[ -f ~/.bashrc ]]; then
  . ~/.bashrc
fi
EOF_PROFILE
fi
chown "${user}:${user}" "${bashrc}" "${profile}"
`
	dataDone := make(chan bool)
	op, err := server.ExecInstance(plan.InstanceName, api.InstanceExecPost{
		Command: []string{"/bin/sh", "-c", script},
		Environment: map[string]string{
			"SANDCASTLE_USER": plan.LinuxUser,
		},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("ensure machine prompt for %s in %s: %w", plan.LinuxUser, plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for machine prompt in %s: %w", plan.InstanceName, err)
	}
	<-dataDone
	return nil
}

type machineCaddyRestarter interface {
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}

func restartMachineCaddy(server machineCaddyRestarter, instanceName string, privateIPWithPrefix string, gateway string) error {
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
		return fmt.Errorf("restart machine Caddy in %s: %w", instanceName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for machine Caddy restart in %s (stderr: %s): %w", instanceName, stderr.String(), err)
	}
	<-dataDone
	return nil
}

func issueMachineCertificateFilesFromProjectCA(server MachineResourceServer, plan machine.CreatePlan) ([]machine.File, error) {
	caCertPEM, err := readProjectCAFile(server, plan, tenant.TenantCACertPath)
	if err != nil {
		return nil, fmt.Errorf("read tenant CA certificate: %w", err)
	}
	caKeyPEM, err := readProjectCAFile(server, plan, tenant.TenantCAKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read tenant CA private key: %w", err)
	}
	files, err := machine.IssueCertificateFiles(plan.Name, plan.Project, plan.Tenant.DNSSuffix, caCertPEM, caKeyPEM)
	if err != nil {
		return nil, err
	}
	return files, nil
}

func readProjectCAFile(server MachineResourceServer, plan machine.CreatePlan, path string) ([]byte, error) {
	content, _, err := server.GetStorageVolumeFile(plan.StoragePool, "custom", plan.CAVolume, path)
	if err != nil {
		return nil, err
	}
	defer content.Close()
	return io.ReadAll(content)
}

func machineRequest(plan machine.CreatePlan) api.InstancesPost {
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
			Description: "Sandcastle machine " + plan.Reference,
			Config:      api.ConfigMap(config),
			Devices:     machineDevicesMap(plan.Devices),
			Profiles:    []string{},
		},
	}
}

func machineDevicesMap(devices map[string]machine.Device) api.DevicesMap {
	output := make(api.DevicesMap, len(devices))
	for name, device := range devices {
		output[name] = map[string]string(device)
	}
	return output
}

type sdkMachineServer struct {
	inner incus.InstanceServer
}

func (s sdkMachineServer) UseProject(name string) MachineResourceServer {
	return sdkMachineResourceServer{inner: s.inner.UseProject(name), projectName: name}
}

type sdkMachineResourceServer struct {
	inner       incus.InstanceServer
	projectName string
}

func (s sdkMachineResourceServer) GetInstance(name string) (*api.Instance, string, error) {
	return s.inner.GetInstance(name)
}

func (s sdkMachineResourceServer) CreateInstance(instance api.InstancesPost) (incus.Operation, error) {
	return s.inner.CreateInstance(instance)
}

func (s sdkMachineResourceServer) UpdateInstanceState(name string, state api.InstanceStatePut, etag string) (incus.Operation, error) {
	return s.inner.UpdateInstanceState(name, state, etag)
}

func (s sdkMachineResourceServer) DeleteInstance(name string) (incus.Operation, error) {
	return s.inner.DeleteInstance(name)
}

func (s sdkMachineResourceServer) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
	return s.inner.CreateInstanceFile(instanceName, path, args)
}

func (s sdkMachineResourceServer) CreateStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error {
	return createStorageVolumeFile(s.inner, s.projectName, pool, volumeType, volumeName, filePath, args)
}

func (s sdkMachineResourceServer) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	return getStorageVolumeFile(s.inner, s.projectName, pool, volumeType, volumeName, filePath)
}

func (s sdkMachineResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	return s.inner.ExecInstance(instanceName, exec, args)
}
