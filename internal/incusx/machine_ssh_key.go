package incusx

import (
	"context"
	"fmt"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type MachineSSHKeyReconciler struct {
	Remote     string
	ConfigPath string
	Store      machine.Store
	Server     MachineSSHKeyServer
}

type MachineSSHKeyServer interface {
	UseProject(name string) MachineSSHKeyResourceServer
}

type MachineSSHKeyResourceServer interface {
	ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error)
}

func NewMachineSSHKeyReconciler(remote string, store machine.Store) MachineSSHKeyReconciler {
	return MachineSSHKeyReconciler{Remote: remote, Store: store}
}

func NewMachineSSHKeyReconcilerForServer(server incus.InstanceServer, store machine.Store) MachineSSHKeyReconciler {
	return MachineSSHKeyReconciler{Server: sdkMachineSSHKeyServer{inner: server}, Store: store}
}

func (r MachineSSHKeyReconciler) ReconcileUserSSHKey(ctx context.Context, summary tenant.Summary, userKey string, publicKey string) error {
	if strings.TrimSpace(publicKey) == "" {
		return fmt.Errorf("User SSH Public Key is required")
	}
	store := r.Store
	if store == nil {
		return fmt.Errorf("machine store is not configured")
	}
	machines, err := store.ListMachines(ctx, summary)
	if err != nil {
		return err
	}
	if len(machines) == 0 {
		return nil
	}
	server, err := r.server()
	if err != nil {
		return err
	}
	projectServer := server.UseProject(summary.IncusName)
	for _, managed := range machines {
		if err := r.reconcileMachine(ctx, projectServer, summary, managed, userKey, publicKey); err != nil {
			return err
		}
	}
	return nil
}

func (r MachineSSHKeyReconciler) reconcileMachine(ctx context.Context, server MachineSSHKeyResourceServer, summary tenant.Summary, managed meta.Machine, userKey string, publicKey string) error {
	instanceName, err := naming.MachineIncusInstanceName(naming.MachineRef{
		Tenant:  summary.Tenant,
		Project: managed.Project,
		Machine: managed.Name,
	})
	if err != nil {
		return err
	}
	linuxUser := managed.LinuxUser
	if strings.TrimSpace(linuxUser) == "" {
		linuxUser = userKey
	}
	script := strings.Join([]string{
		"set -eu",
		`user="${SANDCASTLE_USER:?}"`,
		`key="${SANDCASTLE_SSH_PUBLIC_KEY:?}"`,
		`home="$(getent passwd "$user" | cut -d: -f6)"`,
		`if [ -z "$home" ]; then home="/home/$user"; fi`,
		`ssh_dir="$home/.ssh"`,
		`auth="$ssh_dir/authorized_keys"`,
		`tmp="$auth.tmp"`,
		`install -d -m 0700 -o "$user" -g "$user" "$ssh_dir"`,
		`touch "$auth"`,
		`awk '/^# sandcastle user ssh key begin$/ {skip=1; next} /^# sandcastle user ssh key end$/ {skip=0; next} !skip {print}' "$auth" > "$tmp"`,
		`printf '%s\n%s\n%s\n' '# sandcastle user ssh key begin' "$key" '# sandcastle user ssh key end' >> "$tmp"`,
		`install -m 0600 -o "$user" -g "$user" "$tmp" "$auth"`,
		`rm -f "$tmp"`,
	}, "\n")
	dataDone := make(chan bool)
	op, err := server.ExecInstance(instanceName, api.InstanceExecPost{
		Command: []string{"/bin/sh", "-c", script},
		Environment: map[string]string{
			"SANDCASTLE_USER":           linuxUser,
			"SANDCASTLE_SSH_PUBLIC_KEY": strings.TrimSpace(publicKey),
		},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("reconcile User SSH Public Key on machine %s: %w", instanceName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for User SSH Public Key reconciliation on machine %s: %w", instanceName, err)
	}
	<-dataDone
	return nil
}

func (r MachineSSHKeyReconciler) server() (MachineSSHKeyServer, error) {
	if r.Server != nil {
		return r.Server, nil
	}
	loaded, err := cliconfig.LoadConfig(r.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load Incus config: %w", err)
	}
	remote := r.Remote
	if remote == "" {
		remote = loaded.DefaultRemote
	}
	server, err := loaded.GetInstanceServer(remote)
	if err != nil {
		return nil, fmt.Errorf("connect to Incus remote %q: %w", remote, err)
	}
	return sdkMachineSSHKeyServer{inner: server}, nil
}

type sdkMachineSSHKeyServer struct {
	inner incus.InstanceServer
}

func (s sdkMachineSSHKeyServer) UseProject(name string) MachineSSHKeyResourceServer {
	return sdkMachineSSHKeyResourceServer{inner: s.inner.UseProject(name)}
}

type sdkMachineSSHKeyResourceServer struct {
	inner incus.InstanceServer
}

func (s sdkMachineSSHKeyResourceServer) ExecInstance(instanceName string, exec api.InstanceExecPost, args *incus.InstanceExecArgs) (incus.Operation, error) {
	return s.inner.ExecInstance(instanceName, exec, args)
}
