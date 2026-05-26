package incusx

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/thieso2/sandcastle-incus/internal/certs"
	"github.com/thieso2/sandcastle-incus/internal/config"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type fakeMachineServer struct {
	resource *fakeMachineResource
}

func (s fakeMachineServer) UseProject(name string) MachineResourceServer {
	return s.resource
}

type fakeMachineResource struct {
	instance          *api.Instance
	created           *api.InstancesPost
	updated           *api.InstancePut
	helperCreated     *api.InstancesPost
	helperDeleted     bool
	started           bool
	createdFiles      map[string]string
	createdVolumeDirs []string
	caFiles           map[string]string
	execCommands      [][]string
	execEnvs          []map[string]string
	execStdin         []string
	volumeFileErr     error
}

func (r *fakeMachineResource) GetInstance(name string) (*api.Instance, string, error) {
	if r.instance != nil {
		return r.instance, "etag", nil
	}
	return nil, "", api.StatusErrorf(http.StatusNotFound, "not found")
}

func (r *fakeMachineResource) CreateInstance(instance api.InstancesPost) (incus.Operation, error) {
	if strings.HasPrefix(instance.Name, "sc-storage-init-") {
		r.helperCreated = &instance
		return fakeOperation{}, nil
	}
	r.created = &instance
	r.instance = &api.Instance{Name: instance.Name, StatusCode: api.Running, InstancePut: api.InstancePut{Config: api.ConfigMap(instance.Config)}}
	return fakeOperation{}, nil
}

func (r *fakeMachineResource) UpdateInstance(name string, instance api.InstancePut, etag string) (incus.Operation, error) {
	r.updated = &instance
	if r.instance == nil {
		r.instance = &api.Instance{Name: name}
	}
	r.instance.InstancePut.Config = instance.Config
	return fakeOperation{}, nil
}

func (r *fakeMachineResource) UpdateInstanceState(name string, state api.InstanceStatePut, etag string) (incus.Operation, error) {
	if state.Action == "start" {
		r.started = true
	}
	return fakeOperation{}, nil
}

func (r *fakeMachineResource) DeleteInstance(name string) (incus.Operation, error) {
	if strings.HasPrefix(name, "sc-storage-init-") {
		r.helperDeleted = true
		return fakeOperation{}, nil
	}
	return nil, api.StatusErrorf(http.StatusNotFound, "not found")
}

func (r *fakeMachineResource) CreateInstanceFile(instanceName string, path string, args incus.InstanceFileArgs) error {
	if r.createdFiles == nil {
		r.createdFiles = map[string]string{}
	}
	if args.Content == nil {
		r.createdFiles[path] = args.Type
		return nil
	}
	content, err := io.ReadAll(args.Content)
	if err != nil {
		return err
	}
	r.createdFiles[path] = string(content)
	return nil
}

func (r *fakeMachineResource) CreateStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string, args incus.InstanceFileArgs) error {
	if r.volumeFileErr != nil {
		return r.volumeFileErr
	}
	if args.Type != "directory" {
		return api.StatusErrorf(http.StatusBadRequest, "unexpected volume file type")
	}
	if r.created != nil {
		return api.StatusErrorf(http.StatusBadRequest, "volume directory created after instance")
	}
	r.createdVolumeDirs = append(r.createdVolumeDirs, volumeName+"/"+filePath)
	return nil
}

func (r *fakeMachineResource) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	content, ok := r.caFiles[filePath]
	if !ok {
		return nil, nil, api.StatusErrorf(http.StatusNotFound, "not found")
	}
	return io.NopCloser(strings.NewReader(content)), &incus.InstanceFileResponse{Type: "file"}, nil
}

func (r *fakeMachineResource) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	r.execCommands = append(r.execCommands, exec.Command)
	r.execEnvs = append(r.execEnvs, exec.Environment)
	if args.Stdin != nil {
		content, err := io.ReadAll(args.Stdin)
		if err != nil {
			return nil, err
		}
		r.execStdin = append(r.execStdin, string(content))
	} else {
		r.execStdin = append(r.execStdin, "")
	}
	if args.DataDone != nil {
		close(args.DataDone)
	}
	return fakeOperation{}, nil
}

func TestMachineCreatorCreatesInstance(t *testing.T) {
	plan := machinePlanForTest(t)
	resource := fakeMachineResourceWithCA(t)
	creator := MachineCreator{Server: fakeMachineServer{resource: resource}}
	if err := creator.CreateMachine(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if resource.created == nil {
		t.Fatal("expected instance creation")
	}
	if resource.created.Name != "default-codex" {
		t.Fatalf("created name = %q", resource.created.Name)
	}
	if resource.created.Devices["eth0"]["ipv4.address"] != "10.248.0.20" {
		t.Fatalf("devices = %#v", resource.created.Devices)
	}
	if resource.created.Devices["home"]["path"] != "/home/acme" {
		t.Fatalf("home device = %#v", resource.created.Devices["home"])
	}
	wantVolumeDirs := []string{"sc-home/default", "sc-workspace/default"}
	if strings.Join(resource.createdVolumeDirs, ",") != strings.Join(wantVolumeDirs, ",") {
		t.Fatalf("created volume dirs = %#v, want %#v", resource.createdVolumeDirs, wantVolumeDirs)
	}
	if resource.created.Config["environment.SANDCASTLE_USER"] != "acme" {
		t.Fatalf("instance config = %#v", resource.created.Config)
	}
	if _, ok := resource.created.Config["security.nesting"]; ok {
		t.Fatalf("security.nesting is set by default: %#v", resource.created.Config)
	}
	if _, ok := resource.created.Config["security.privileged"]; ok {
		t.Fatalf("security.privileged is set by default: %#v", resource.created.Config)
	}
	if len(resource.execCommands) != 1 {
		t.Fatalf("exec commands = %#v", resource.execCommands)
	}
	if strings.Join(resource.execCommands[0], " ") != "/bin/sh -se" {
		t.Fatalf("machine configure command = %#v", resource.execCommands[0])
	}
	configureScript := resource.execStdin[0]
	for _, want := range []string{
		"step hostname",
		"ssh-keygen -A",
		"/usr/local/bin/sandcastle-bootstrap",
		"cap_net_raw+p /usr/bin/ping",
		"resolv-conf",
	} {
		if !strings.Contains(configureScript, want) {
			t.Fatalf("machine configure script missing %q:\n%s", want, configureScript)
		}
	}
	if resource.execEnvs[0]["SANDCASTLE_HOSTNAME"] != "codex.default.acme" {
		t.Fatalf("machine configure env = %#v", resource.execEnvs[0])
	}
	if resource.execEnvs[0]["SANDCASTLE_USER"] != "acme" {
		t.Fatalf("machine configure env = %#v", resource.execEnvs[0])
	}
	if resource.execEnvs[0]["SANDCASTLE_UID"] != "1000" || resource.execEnvs[0]["SANDCASTLE_GID"] != "1000" {
		t.Fatalf("machine configure uid/gid env = %#v", resource.execEnvs[0])
	}
	for _, want := range []string{
		"step caddy-config",
		"/etc/systemd/system/caddy.service.d/machine.conf",
		machine.CaddyfilePath,
		machine.MachineCertPath,
		machine.MachineCertKeyPath,
		"base64 -d",
		"systemctl daemon-reload",
		"systemctl restart caddy",
		"systemctl is-active caddy",
	} {
		if !strings.Contains(configureScript, want) {
			t.Fatalf("machine configure script missing %q:\n%s", want, configureScript)
		}
	}
	if resource.updated != nil {
		t.Fatalf("machine metadata updated unexpectedly: %#v", resource.updated)
	}
	for _, command := range resource.execCommands {
		if strings.Contains(strings.Join(command, " "), "tailscale") {
			t.Fatalf("unexpected machine-level Tailscale command: %#v", command)
		}
	}
}

func TestMachineCreatorVerboseLogsDurations(t *testing.T) {
	plan := machinePlanForTest(t)
	resource := fakeMachineResourceWithCA(t)
	var output strings.Builder
	creator := MachineCreator{
		Server: fakeMachineServer{resource: resource},
		Log: func(msg string) {
			output.WriteString(msg)
		},
	}
	if err := creator.CreateMachine(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	joined := output.String()
	for _, want := range []string{
		"[machine-create] use project sc-acme ... done (",
		"[machine-create] get instance default-codex ... done (",
		"[machine-create] ensure machine storage dirs for default-codex\n",
		"[machine-create] storage dirs for default-codex: create sc-home/default via volume API ... done (",
		"[machine-create] storage dirs for default-codex: create sc-workspace/default via volume API ... done (",
		"[machine-create] ensure machine storage dirs for default-codex done (",
		"[machine-create] create instance default-codex (image: sandcastle/ai:latest) ... done (",
		"[machine-create] issue certificate from tenant CA started\n",
		"[machine-create] issue certificate from tenant CA done (",
		"[machine-create] configure instance default-codex done (",
		"[machine-create] configure instance default-codex: run machine configure script (2 cert files, 0 workload files) ... done (",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("verbose logs missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "Tailscale Machine IP") {
		t.Fatalf("verbose logs should not mention machine-level Tailscale IP:\n%s", joined)
	}
}

func TestMachineCreatorFallsBackToHelperForStorageDirs(t *testing.T) {
	plan := machinePlanForTest(t)
	resource := fakeMachineResourceWithCA(t)
	resource.volumeFileErr = api.StatusErrorf(http.StatusNotFound, "not found")
	creator := MachineCreator{Server: fakeMachineServer{resource: resource}}
	if err := creator.CreateMachine(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if resource.helperCreated == nil {
		t.Fatal("expected storage helper instance creation")
	}
	if resource.helperCreated.Devices["home"]["source"] != "sc-home" || resource.helperCreated.Devices["workspace"]["source"] != "sc-workspace" {
		t.Fatalf("helper devices = %#v", resource.helperCreated.Devices)
	}
	if !resource.helperDeleted {
		t.Fatal("expected storage helper cleanup")
	}
	if resource.created == nil {
		t.Fatal("expected machine instance creation")
	}
	commands := strings.Join(flattenCommands(resource.execCommands), "\n")
	if !strings.Contains(commands, "install -d -o 1000 -g 1000 -m 0755 -- '/mnt/home/default'") {
		t.Fatalf("helper commands missing home dir: %s", commands)
	}
	if !strings.Contains(commands, "install -d -o 1000 -g 1000 -m 0755 -- '/mnt/workspace/default'") {
		t.Fatalf("helper commands missing workspace dir: %s", commands)
	}
}

func TestMachineCreatorEnablesNestingForContainerTools(t *testing.T) {
	plan := machinePlanForTest(t)
	plan.ContainerTools = true
	resource := fakeMachineResourceWithCA(t)
	creator := MachineCreator{Server: fakeMachineServer{resource: resource}}
	if err := creator.CreateMachine(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if resource.created == nil {
		t.Fatal("expected instance creation")
	}
	if resource.created.Config["security.nesting"] != "true" {
		t.Fatalf("security.nesting = %q", resource.created.Config["security.nesting"])
	}
	if _, ok := resource.created.Config["security.privileged"]; ok {
		t.Fatalf("security.privileged is set: %#v", resource.created.Config)
	}
}

func flattenCommands(commands [][]string) []string {
	flattened := make([]string, 0, len(commands))
	for _, command := range commands {
		flattened = append(flattened, strings.Join(command, " "))
	}
	return flattened
}

func TestMachineCreatorStartsExistingStoppedInstance(t *testing.T) {
	plan := machinePlanForTest(t)
	resource := fakeMachineResourceWithCA(t)
	resource.instance = &api.Instance{Name: plan.InstanceName, StatusCode: api.Stopped, InstancePut: api.InstancePut{Config: api.ConfigMap(plan.MetadataConfig)}}
	creator := MachineCreator{Server: fakeMachineServer{resource: resource}}
	if err := creator.CreateMachine(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if !resource.started {
		t.Fatal("expected stopped instance to be started")
	}
	if len(resource.execStdin) < 1 || !strings.Contains(resource.execStdin[0], machine.CaddyfilePath) {
		t.Fatal("expected Caddyfile in config script")
	}
}

func machinePlanForTest(t *testing.T) machine.CreatePlan {
	t.Helper()
	projectConfig, err := meta.TenantConfig(meta.Tenant{
		Tenant:      "acme",
		Projects:    []meta.Project{{Name: "default"}},
		PrivateCIDR: "10.248.0.0/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	admin := config.LoadAdminFromEnv()
	admin.Tenant = "acme"
	plan, err := machine.PlanCreate(context.Background(), admin, tenant.MemoryStore{Projects: []tenant.IncusProject{{
		Name:   "sc-acme",
		Config: projectConfig,
	}}}, nil, machine.CreateRequest{Reference: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func fakeMachineResourceWithCA(t *testing.T) *fakeMachineResource {
	t.Helper()
	ca, err := certs.GenerateCA("test CA", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return &fakeMachineResource{caFiles: map[string]string{
		tenant.TenantCACertPath: string(ca.CertificatePEM),
		tenant.TenantCAKeyPath:  string(ca.PrivateKeyPEM),
	}}
}
