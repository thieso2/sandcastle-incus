package incusx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os/exec"
	"path"
	"strings"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type MachineCreateServer interface {
	UseProject(name string) MachineResourceServer
}

type MachineResourceServer interface {
	GetInstance(name string) (*api.Instance, string, error)
	CreateInstance(instance api.InstancesPost) (incus.Operation, error)
	UpdateInstance(name string, instance api.InstancePut, ETag string) (incus.Operation, error)
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
		c.Log = func(msg string) { fmt.Fprint(w, msg) }
	}
	return c
}

func (c MachineCreator) log(msg string) {
	if c.Log != nil {
		c.Log("[machine-create] " + msg + "\n")
	}
}

func (c MachineCreator) runCommand(label string, fn func() error) error {
	return c.runCommandWithExpectedError(label, nil, fn)
}

func (c MachineCreator) runCommandWithExpectedError(label string, expected func(error) bool, fn func() error) error {
	if c.Log == nil {
		return fn()
	}
	start := time.Now()
	c.Log("[machine-create] " + label + " ...")
	if err := fn(); err != nil {
		if expected != nil && expected(err) {
			c.Log(fmt.Sprintf(" done (%s)\n", formatVerboseDuration(time.Since(start))))
			return err
		}
		c.Log(fmt.Sprintf(" failed (%s)\n", formatVerboseDuration(time.Since(start))))
		return err
	}
	c.Log(fmt.Sprintf(" done (%s)\n", formatVerboseDuration(time.Since(start))))
	return nil
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
		var instanceServer incus.InstanceServer
		if err := c.runCommand("connect to Incus remote "+remote, func() error {
			var err error
			instanceServer, err = loaded.GetInstanceServer(remote)
			return err
		}); err != nil {
			return fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkMachineServer{inner: instanceServer}
	}
	var projectServer MachineResourceServer
	if err := c.runCommand("use project "+plan.Tenant.IncusName, func() error {
		projectServer = server.UseProject(plan.Tenant.IncusName)
		return nil
	}); err != nil {
		return err
	}
	var instance *api.Instance
	var instanceETag string
	err := c.runCommandWithExpectedError("get instance "+plan.InstanceName, func(err error) bool {
		return api.StatusErrorCheck(err, http.StatusNotFound)
	}, func() error {
		var err error
		instance, instanceETag, err = projectServer.GetInstance(plan.InstanceName)
		return err
	})
	if err == nil {
		if err := c.updateMachineConfigStep(projectServer, instance, instanceETag, plan); err != nil {
			return err
		}
		if plan.StartsByDefault && !instance.IsActive() {
			if err := c.runCommand("start instance "+plan.InstanceName, func() error {
				op, err := projectServer.UpdateInstanceState(plan.InstanceName, api.InstanceStatePut{Action: "start", Timeout: -1}, "")
				if err != nil {
					return err
				}
				return op.Wait()
			}); err != nil {
				return fmt.Errorf("start machine %s: %w", plan.InstanceName, err)
			}
		}
		if err := c.configureMachineStep(projectServer, plan); err != nil {
			return err
		}
		return nil
	}
	if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("get machine %s: %w", plan.InstanceName, err)
	}
	certificateResult, certificateStart := c.issueMachineCertificateFilesAsync(projectServer, plan)
	if err := c.ensureMachineStorageDirsStep(projectServer, plan); err != nil {
		return err
	}
	if err := c.runCommand("create instance "+plan.InstanceName+" (image: "+plan.ImageAlias+")", func() error {
		op, err := projectServer.CreateInstance(machineRequest(plan))
		if err != nil {
			return err
		}
		return op.Wait()
	}); err != nil {
		return fmt.Errorf("create machine %s: %w", plan.InstanceName, err)
	}
	if certificateResult != nil {
		result := <-certificateResult
		if result.err != nil {
			if c.Log != nil {
				c.log(fmt.Sprintf("issue certificate from tenant CA failed (%s)", formatVerboseDuration(time.Since(certificateStart))))
			}
			return result.err
		}
		if c.Log != nil {
			c.log(fmt.Sprintf("issue certificate from tenant CA done (%s)", formatVerboseDuration(time.Since(certificateStart))))
		}
		plan.CertificateFiles = result.files
	}
	if err := c.configureMachineStep(projectServer, plan); err != nil {
		return err
	}
	return nil
}

func (c MachineCreator) updateMachineConfigStep(server MachineResourceServer, instance *api.Instance, etag string, plan machine.CreatePlan) error {
	current := map[string]string(instance.Config)
	if current[meta.KeyKind] != meta.KindMachine {
		return nil
	}
	desired := machineConfigUpdates(plan)
	updated := make(map[string]string, len(current)+len(desired))
	for key, value := range current {
		updated[key] = value
	}
	changed := false
	for key, value := range desired {
		if updated[key] != value {
			changed = true
		}
		updated[key] = value
	}
	if !changed {
		return nil
	}
	return c.runCommand("update machine metadata "+plan.InstanceName, func() error {
		put := instance.Writable()
		put.Config = api.ConfigMap(updated)
		op, err := server.UpdateInstance(plan.InstanceName, put, etag)
		if err != nil {
			return err
		}
		return op.Wait()
	})
}

type machineCertificateIssueResult struct {
	files []machine.File
	err   error
}

func (c MachineCreator) issueMachineCertificateFilesAsync(server MachineResourceServer, plan machine.CreatePlan) (<-chan machineCertificateIssueResult, time.Time) {
	if len(plan.CertificateFiles) > 0 {
		return nil, time.Time{}
	}
	start := time.Now()
	if c.Log != nil {
		c.log("issue certificate from tenant CA started")
	}
	result := make(chan machineCertificateIssueResult, 1)
	go func() {
		files, err := issueMachineCertificateFilesFromProjectCA(server, plan)
		result <- machineCertificateIssueResult{files: files, err: err}
	}()
	return result, start
}

func (c MachineCreator) configureMachine(server MachineResourceServer, plan machine.CreatePlan) error {
	if len(plan.WorkloadFiles) == 0 {
		c.log("workload identity files: none")
	} else {
		c.log(fmt.Sprintf("workload identity files: %d (%s)", len(plan.WorkloadFiles), strings.Join(machineFilePaths(plan.WorkloadFiles), ",")))
	}
	return ensureMachineFiles(server, plan, func(label string, fn func() error) error {
		return c.runCommand("configure instance "+plan.InstanceName+": "+label, fn)
	})
}

func (c MachineCreator) ensureMachineStorageDirsStep(server MachineResourceServer, plan machine.CreatePlan) error {
	if c.Log == nil {
		return ensureMachineStorageDirs(server, plan, nil)
	}
	start := time.Now()
	c.log("ensure machine storage dirs for " + plan.InstanceName)
	if err := ensureMachineStorageDirs(server, plan, func(label string, expected func(error) bool, fn func() error) error {
		return c.runCommandWithExpectedError("storage dirs for "+plan.InstanceName+": "+label, expected, fn)
	}); err != nil {
		c.log(fmt.Sprintf("ensure machine storage dirs for %s failed (%s)", plan.InstanceName, formatVerboseDuration(time.Since(start))))
		return err
	}
	c.log(fmt.Sprintf("ensure machine storage dirs for %s done (%s)", plan.InstanceName, formatVerboseDuration(time.Since(start))))
	return nil
}

func (c MachineCreator) configureMachineStep(server MachineResourceServer, plan machine.CreatePlan) error {
	if c.Log == nil {
		return c.configureMachine(server, plan)
	}
	start := time.Now()
	if err := c.configureMachine(server, plan); err != nil {
		c.log(fmt.Sprintf("configure instance %s failed (%s)", plan.InstanceName, formatVerboseDuration(time.Since(start))))
		return err
	}
	c.log(fmt.Sprintf("configure instance %s done (%s)", plan.InstanceName, formatVerboseDuration(time.Since(start))))
	return nil
}

type machineStorageStepRunner func(label string, expected func(error) bool, fn func() error) error

func runMachineStorageStep(run machineStorageStepRunner, label string, expected func(error) bool, fn func() error) error {
	if run == nil {
		return fn()
	}
	return run(label, expected, fn)
}

func ensureMachineStorageDirs(server MachineResourceServer, plan machine.CreatePlan, run machineStorageStepRunner) error {
	var helperDirs []machineStorageDir
	for _, volumeDir := range []machineStorageDir{
		{volume: tenant.HomeVolumeName, path: plan.HomeDir},
		{volume: tenant.WorkspaceVolumeName, path: plan.WorkspaceDir},
	} {
		if volumeDir.path == "" || volumeDir.path == "." {
			continue
		}
		err := runMachineStorageStep(run, "create "+volumeDir.volume+"/"+volumeDir.path+" via volume API", func(err error) bool {
			return api.StatusErrorCheck(err, http.StatusNotFound)
		}, func() error {
			return server.CreateStorageVolumeFile(plan.StoragePool, "custom", volumeDir.volume, volumeDir.path, incus.InstanceFileArgs{
				Type: "directory",
				UID:  int64(machine.DefaultLinuxUID),
				GID:  int64(machine.DefaultLinuxGID),
				Mode: 0o755,
			})
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
		return ensureMachineStorageDirsWithHelper(server, plan, helperDirs, run)
	}
	return nil
}

type machineStorageDir struct {
	volume string
	path   string
}

func ensureMachineStorageDirsWithHelper(server MachineResourceServer, plan machine.CreatePlan, dirs []machineStorageDir, run machineStorageStepRunner) error {
	name := machineStorageHelperName(plan.InstanceName)
	_ = runMachineStorageStep(run, "delete stale helper "+name, nil, func() error {
		return deleteMachineStorageHelper(server, name)
	})
	helper := machineStorageHelperRequest(plan, name)
	var op incus.Operation
	if err := runMachineStorageStep(run, "create helper "+name, nil, func() error {
		var err error
		op, err = server.CreateInstance(helper)
		if err != nil {
			return fmt.Errorf("create machine storage helper %s: %w", name, err)
		}
		if err := op.Wait(); err != nil {
			return fmt.Errorf("wait for machine storage helper %s: %w", name, err)
		}
		return nil
	}); err != nil {
		_ = deleteMachineStorageHelper(server, name)
		return err
	}
	defer func() {
		_ = runMachineStorageStep(run, "delete helper "+name, nil, func() error {
			return deleteMachineStorageHelper(server, name)
		})
	}()

	var commands []string
	for _, dir := range dirs {
		target := "/mnt/" + storageHelperMountName(dir.volume) + "/" + path.Clean(dir.path)
		commands = append(commands, "install -d -o 1000 -g 1000 -m 0755 -- "+shellQuote(target))
	}
	dataDone := make(chan bool)
	return runMachineStorageStep(run, "create directories with helper "+name, nil, func() error {
		op, err := server.ExecInstance(name, api.InstanceExecPost{
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
	})
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

func boolEnv(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

type machineConfigStepRunner func(label string, fn func() error) error

func runMachineConfigStep(run machineConfigStepRunner, label string, fn func() error) error {
	if run == nil {
		return fn()
	}
	return run(label, fn)
}

func ensureMachineFiles(server MachineResourceServer, plan machine.CreatePlan, run machineConfigStepRunner) error {
	certificateFiles := plan.CertificateFiles
	if certificateFiles == nil {
		if err := runMachineConfigStep(run, "issue certificate from tenant CA", func() error {
			var err error
			certificateFiles, err = issueMachineCertificateFilesFromProjectCA(server, plan)
			return err
		}); err != nil {
			return err
		}
	}
	return runMachineConfigStep(run, fmt.Sprintf("run machine configure script (%d cert files, %d workload files)", len(certificateFiles), len(plan.WorkloadFiles)), func() error {
		return runMachineConfigureScript(server, plan, certificateFiles)
	})
}

func machineFilePaths(files []machine.File) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths
}

func runMachineConfigureScript(server MachineResourceServer, plan machine.CreatePlan, certificateFiles []machine.File) error {
	ipWithPrefix, gateway, err := machineNetworkParams(plan)
	if err != nil {
		return err
	}
	files := []machine.File{
		{
			Path:    "/etc/systemd/system/caddy.service.d/machine.conf",
			Content: []byte("[Service]\nUser=root\nGroup=root\n"),
			Mode:    0o644,
		},
		{
			Path:    plan.CaddyFile.Path,
			Content: []byte(plan.CaddyFile.Content),
			Mode:    plan.CaddyFile.Mode,
		},
	}
	files = append(files, certificateFiles...)
	files = append(files, plan.WorkloadFiles...)

	var script strings.Builder
	script.WriteString(`set -eu
step() { printf 'step=%s\n' "$1" >&2; }

step hostname
if [ -n "${SANDCASTLE_HOSTNAME:-}" ]; then
  printf '%s\n' "${SANDCASTLE_HOSTNAME}" >/etc/hostname
  hostname "${SANDCASTLE_HOSTNAME}"
fi

step ssh-host-keys
marker=/var/lib/sandcastle/ssh-host-keys-generated
if [ ! -e "${marker}" ]; then
  install -d -m 0755 /var/lib/sandcastle
  rm -f /etc/ssh/ssh_host_*_key /etc/ssh/ssh_host_*_key.pub
  ssh-keygen -A
  touch "${marker}"
  systemctl try-restart ssh.service 2>/dev/null || systemctl try-restart ssh 2>/dev/null || true
fi

step linux-user
/usr/local/bin/sandcastle-bootstrap

step ping-capability
if command -v setcap >/dev/null 2>&1 && [ -x /usr/bin/ping ]; then
  setcap cap_net_raw+p /usr/bin/ping
fi

step docker
if [ -e /lib/systemd/system/docker.service ] || command -v docker >/dev/null 2>&1; then
  if [ "${SANDCASTLE_DOCKER_AUTOSTART:-0}" = "1" ]; then
    systemctl enable --now containerd.service docker.service >/dev/null 2>&1 || true
  else
    systemctl disable docker.service docker.socket containerd.service >/dev/null 2>&1 || true
  fi
fi

step tailscale
if command -v tailscaled >/dev/null 2>&1 || [ -e /lib/systemd/system/tailscaled.service ]; then
  pkill -x tailscaled >/dev/null 2>&1 || true
  systemctl disable --now tailscaled.service >/dev/null 2>&1 || true
  systemctl mask tailscaled.service >/dev/null 2>&1 || true
fi

step resolv-conf
if [ -n "${SANDCASTLE_DNS_ADDRESS:-}" ]; then
  rm -f /etc/resolv.conf
  printf 'nameserver %s\n' "${SANDCASTLE_DNS_ADDRESS}" >/etc/resolv.conf
  chmod 0644 /etc/resolv.conf
fi
`)
	appendMachineCaddyConfigScript(&script, files, ipWithPrefix, gateway)
	var stderr bytes.Buffer
	dataDone := make(chan bool)
	gitName, gitEmail := localGitIdentity()
	op, err := server.ExecInstance(plan.InstanceName, api.InstanceExecPost{
		Command: []string{"/bin/sh", "-se"},
		Environment: map[string]string{
			"SANDCASTLE_USER":             plan.LinuxUser,
			"SANDCASTLE_UID":              fmt.Sprintf("%d", machine.DefaultLinuxUID),
			"SANDCASTLE_GID":              fmt.Sprintf("%d", machine.DefaultLinuxGID),
			"SANDCASTLE_SSH_PUBLIC_KEY":   plan.SSHPublicKey,
			"SANDCASTLE_HOSTNAME":         plan.Hostname,
			"SANDCASTLE_DNS_ADDRESS":      plan.Tenant.DNSAddress,
			"SANDCASTLE_DOCKER_AUTOSTART": boolEnv(plan.DockerAutostart),
			"SANDCASTLE_GIT_NAME":         gitName,
			"SANDCASTLE_GIT_EMAIL":        gitEmail,
		},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(script.String()),
		Stderr:   &stderr,
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("run machine configure script in %s: %w", plan.InstanceName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for machine configure script in %s (stderr: %s): %w", plan.InstanceName, stderr.String(), err)
	}
	<-dataDone
	return nil
}

// localGitIdentity resolves the git identity of the CLI user running the
// create, so each machine's tenant owner gets the same name/email in
// ~/.gitconfig. Empty values are fine: the bootstrap skips whatever is unset.
func localGitIdentity() (name string, email string) {
	return gitConfigValue("user.name"), gitConfigValue("user.email")
}

func gitConfigValue(key string) string {
	out, err := exec.Command("git", "config", "--get", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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

func appendMachineCaddyConfigScript(script *strings.Builder, files []machine.File, ipWithPrefix string, gateway string) {
	script.WriteString("step caddy-config\n")
	script.WriteString("write_file() {\n")
	script.WriteString("  path=\"$1\"\n")
	script.WriteString("  mode=\"$2\"\n")
	script.WriteString("  install -d -m 0755 \"$(dirname \"$path\")\"\n")
	script.WriteString("  base64 -d >\"$path\"\n")
	script.WriteString("  chmod \"$mode\" \"$path\"\n")
	script.WriteString("  case \"$path\" in\n")
	script.WriteString("    /var/lib/sandcastle/workload/runtime-secret|/var/lib/sandcastle/workload/gcp-credential.json) chown \"${SANDCASTLE_USER}:${SANDCASTLE_USER}\" \"$path\" 2>/dev/null || true ;;\n")
	script.WriteString("  esac\n")
	script.WriteString("}\n")
	for _, file := range files {
		script.WriteString("write_file ")
		script.WriteString(shellQuote(file.Path))
		script.WriteString(" ")
		script.WriteString(shellQuote(fmt.Sprintf("%04o", file.Mode)))
		script.WriteString(" <<'EOF_FILE'\n")
		script.WriteString(base64.StdEncoding.EncodeToString(file.Content))
		script.WriteString("\nEOF_FILE\n")
	}
	if ipWithPrefix != "" && gateway != "" {
		appendMachineNetworkConfigScript(script, ipWithPrefix, gateway)
	}
	script.WriteString("systemctl daemon-reload\n")
	script.WriteString("systemctl restart caddy\n")
	script.WriteString("for i in $(seq 1 50); do systemctl is-active caddy >/dev/null 2>&1 && exit 0; sleep 0.1; done\n")
	script.WriteString("systemctl is-active caddy\n")
}

func appendMachineNetworkConfigScript(script *strings.Builder, ipWithPrefix string, gateway string) {
	script.WriteString("step machine-network\n")
	script.WriteString("install -d -m 0755 /usr/local/sbin /etc/systemd/system\n")
	script.WriteString("cat >/usr/local/sbin/sandcastle-machine-network <<'EOF_NETWORK'\n")
	script.WriteString("#!/bin/sh\n")
	script.WriteString("set -eu\n")
	script.WriteString("/usr/sbin/ip link set eth0 up\n")
	script.WriteString("/usr/sbin/ip addr replace ")
	script.WriteString(ipWithPrefix)
	script.WriteString(" dev eth0\n")
	script.WriteString("/usr/sbin/ip route replace default via ")
	script.WriteString(gateway)
	script.WriteString("\n")
	script.WriteString("EOF_NETWORK\n")
	script.WriteString("chmod 0755 /usr/local/sbin/sandcastle-machine-network\n")
	script.WriteString("cat >/etc/systemd/system/sandcastle-machine-network.service <<'EOF_UNIT'\n")
	script.WriteString("[Unit]\n")
	script.WriteString("Description=Sandcastle machine static network\n")
	script.WriteString("After=network-pre.target\n")
	script.WriteString("Before=network-online.target\n")
	script.WriteString("\n")
	script.WriteString("[Service]\n")
	script.WriteString("Type=oneshot\n")
	script.WriteString("ExecStart=/usr/local/sbin/sandcastle-machine-network\n")
	script.WriteString("RemainAfterExit=yes\n")
	script.WriteString("\n")
	script.WriteString("[Install]\n")
	script.WriteString("WantedBy=multi-user.target\n")
	script.WriteString("EOF_UNIT\n")
	script.WriteString("/usr/local/sbin/sandcastle-machine-network\n")
	script.WriteString("systemctl daemon-reload\n")
	script.WriteString("systemctl enable sandcastle-machine-network.service\n")
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
	config := machineConfigUpdates(plan)
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

func machineConfigUpdates(plan machine.CreatePlan) map[string]string {
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
	return config
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

func (s sdkMachineResourceServer) UpdateInstance(name string, instance api.InstancePut, etag string) (incus.Operation, error) {
	return s.inner.UpdateInstance(name, instance, etag)
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
