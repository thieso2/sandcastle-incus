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

// MachinePasswordReconciler sets the Unix login and Samba password for the
// tenant owner's Linux user on every machine in a tenant. It reuses the
// SSH-key exec server abstraction (ExecInstance per project), mirroring
// MachineSSHKeyReconciler.
type MachinePasswordReconciler struct {
	Remote     string
	ConfigPath string
	Store      machine.Store
	Server     MachineSSHKeyServer
}

func NewMachinePasswordReconciler(remote string, store machine.Store) MachinePasswordReconciler {
	return MachinePasswordReconciler{Remote: remote, Store: store}
}

func NewMachinePasswordReconcilerForServer(server incus.InstanceServer, store machine.Store) MachinePasswordReconciler {
	return MachinePasswordReconciler{Server: sdkMachineSSHKeyServer{inner: server}, Store: store}
}

func (r MachinePasswordReconciler) ReconcileTenantPassword(ctx context.Context, summary tenant.Summary, password string) error {
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("password is required")
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
		if err := r.reconcileMachine(ctx, projectServer, summary, managed, password); err != nil {
			return err
		}
	}
	return nil
}

func (r MachinePasswordReconciler) reconcileMachine(_ context.Context, server MachineSSHKeyResourceServer, summary tenant.Summary, managed meta.Machine, password string) error {
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
		linuxUser = summary.UnixUser
	}
	if strings.TrimSpace(linuxUser) == "" {
		return fmt.Errorf("machine %s has no Linux user to set a password for", instanceName)
	}
	// The password is passed via the environment (never on the command line) so
	// it does not leak into the machine's process table. smbpasswd may be absent
	// on machines built from an older base image, so guard on it.
	script := strings.Join([]string{
		"set -eu",
		`user="${SANDCASTLE_USER:?}"`,
		`pw="${SANDCASTLE_PASSWORD:?}"`,
		`printf '%s:%s\n' "$user" "$pw" | chpasswd`,
		`if command -v smbpasswd >/dev/null 2>&1; then`,
		`  printf '%s\n%s\n' "$pw" "$pw" | smbpasswd -s -a "$user"`,
		`  smbpasswd -e "$user" >/dev/null 2>&1 || true`,
		`fi`,
	}, "\n")
	dataDone := make(chan bool)
	op, err := server.ExecInstance(instanceName, api.InstanceExecPost{
		Command: []string{"/bin/sh", "-c", script},
		Environment: map[string]string{
			"SANDCASTLE_USER":     linuxUser,
			"SANDCASTLE_PASSWORD": password,
		},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{
		Stdin:    strings.NewReader(""),
		DataDone: dataDone,
	})
	if err != nil {
		return fmt.Errorf("set password on machine %s: %w", instanceName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for password update on machine %s: %w", instanceName, err)
	}
	<-dataDone
	return nil
}

func (r MachinePasswordReconciler) server() (MachineSSHKeyServer, error) {
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
