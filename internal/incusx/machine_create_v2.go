package incusx

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

// CreateMachineV2Request describes a v2 machine launch: a stock cloud image
// into one of the tenant's app Incus projects. The machine is a freeform Incus
// instance — no Sandcastle metadata; the project's default profile supplies the
// shared-bridge NIC, the cloud-init login user + SSH key, and the /workspace
// volume, and the auth-app reconciler auto-registers its DNS record.
type CreateMachineV2Request struct {
	IncusProject string
	Name         string
	Image        string
	VM           bool
}

type CreateMachineV2Result struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Project   string `json:"incusProject"`
	Image     string `json:"image"`
	PrivateIP string `json:"privateIP,omitempty"`
	LoginUser string `json:"loginUser,omitempty"`
}

// CreateMachineV2 launches the instance and waits (bounded) for it to lease an
// IPv4 address on the tenant bridge. An empty PrivateIP in the result means the
// machine is still booting — not an error.
func (c TenantCreator) CreateMachineV2(ctx context.Context, request CreateMachineV2Request) (CreateMachineV2Result, error) {
	server, err := c.resolveV2Server()
	if err != nil {
		return CreateMachineV2Result{}, err
	}
	project := server.UseProject(request.IncusProject)
	if _, _, err := project.GetInstance(request.Name); err == nil {
		return CreateMachineV2Result{}, fmt.Errorf("machine %q already exists in project %s", request.Name, request.IncusProject)
	} else if !api.StatusErrorCheck(err, http.StatusNotFound) {
		return CreateMachineV2Result{}, fmt.Errorf("get machine %s: %w", request.Name, err)
	}
	instanceType := api.InstanceTypeContainer
	if request.VM {
		instanceType = api.InstanceTypeVM
	}
	result := CreateMachineV2Result{
		Name:      request.Name,
		Type:      string(instanceType),
		Project:   request.IncusProject,
		Image:     request.Image,
		LoginUser: v2ProfileLoginUser(project),
	}
	c.log("launching " + result.Type + " " + request.Name + " from " + request.Image + " into " + request.IncusProject)
	op, err := project.CreateInstance(api.InstancesPost{
		Name:   request.Name,
		Type:   instanceType,
		Start:  true,
		Source: imageInstanceSource(request.Image),
	})
	if err != nil {
		return CreateMachineV2Result{}, fmt.Errorf("create machine %s: %w", request.Name, err)
	}
	if err := op.Wait(); err != nil {
		return CreateMachineV2Result{}, fmt.Errorf("wait for machine %s: %w", request.Name, err)
	}
	result.PrivateIP = waitForV2InstanceIPv4(ctx, project, request.Name, v2MachineIPTimeout(request.VM))
	return result, nil
}

// EnsureMachineV2Result reports what EnsureMachineV2 had to do to make the
// machine reachable: created from scratch, started from stopped, or nothing.
type EnsureMachineV2Result struct {
	Name      string `json:"name"`
	Project   string `json:"incusProject"`
	Created   bool   `json:"created"`
	Started   bool   `json:"started"`
	PrivateIP string `json:"privateIP,omitempty"`
	LoginUser string `json:"loginUser"`
}

// EnsureMachineV2 makes the named v2 machine exist and run: creates it from the
// request image when missing, starts it when stopped, and waits (bounded) for
// an IPv4 lease. LoginUser is read from the project default profile so callers
// can open an SSH session as the right user.
func (c TenantCreator) EnsureMachineV2(ctx context.Context, request CreateMachineV2Request) (EnsureMachineV2Result, error) {
	server, err := c.resolveV2Server()
	if err != nil {
		return EnsureMachineV2Result{}, err
	}
	project := server.UseProject(request.IncusProject)
	result := EnsureMachineV2Result{
		Name:      request.Name,
		Project:   request.IncusProject,
		LoginUser: v2ProfileLoginUser(project),
	}
	instance, _, err := project.GetInstance(request.Name)
	switch {
	case err == nil && instance.StatusCode == api.Stopped:
		op, err := project.UpdateInstanceState(request.Name, api.InstanceStatePut{Action: "start", Timeout: -1}, "")
		if err != nil {
			return EnsureMachineV2Result{}, fmt.Errorf("start machine %s: %w", request.Name, err)
		}
		if err := op.Wait(); err != nil {
			return EnsureMachineV2Result{}, fmt.Errorf("wait for machine %s start: %w", request.Name, err)
		}
		result.Started = true
		result.PrivateIP = waitForV2InstanceIPv4(ctx, project, request.Name, v2MachineIPTimeout(request.VM))
	case err == nil:
		result.PrivateIP = waitForV2InstanceIPv4(ctx, project, request.Name, 20*time.Second)
	case api.StatusErrorCheck(err, http.StatusNotFound):
		created, err := c.CreateMachineV2(ctx, request)
		if err != nil {
			return EnsureMachineV2Result{}, err
		}
		result.Created = true
		result.PrivateIP = created.PrivateIP
	default:
		return EnsureMachineV2Result{}, fmt.Errorf("get machine %s: %w", request.Name, err)
	}
	return result, nil
}

// v2ProfileLoginUser extracts the login username from the project default
// profile's cloud-init user-data (the first `- name:` entry).
func v2ProfileLoginUser(project TenantResourceServer) string {
	profile, _, err := project.GetProfile("default")
	if err == nil {
		if match := v2ProfileUserPattern.FindStringSubmatch(profile.Config["cloud-init.user-data"]); match != nil {
			return match[1]
		}
	}
	return tenant.DefaultV2UnixUser
}

var v2ProfileUserPattern = regexp.MustCompile(`(?m)^\s*-\s*name:\s*(\S+)`)

// MachineLifecycleV2 applies start/stop/restart/delete to a freeform v2
// machine. Delete force-stops a running instance first; state changes go
// through the normal instance-state API.
func (c TenantCreator) MachineLifecycleV2(ctx context.Context, incusProject string, name string, action string) error {
	server, err := c.resolveV2Server()
	if err != nil {
		return err
	}
	project := server.UseProject(incusProject)
	instance, _, err := project.GetInstance(name)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return fmt.Errorf("machine %q not found in project %s", name, incusProject)
		}
		return fmt.Errorf("get machine %s: %w", name, err)
	}
	switch action {
	case "delete":
		if instance.StatusCode != api.Stopped {
			if op, err := project.UpdateInstanceState(name, api.InstanceStatePut{Action: "stop", Force: true}, ""); err == nil {
				_ = op.Wait()
			}
		}
		op, err := project.DeleteInstance(name)
		if err != nil {
			return fmt.Errorf("delete machine %s: %w", name, err)
		}
		if err := op.Wait(); err != nil {
			return fmt.Errorf("wait for machine %s deletion: %w", name, err)
		}
		return nil
	case "start", "stop", "restart":
		op, err := project.UpdateInstanceState(name, api.InstanceStatePut{Action: action, Force: action != "start", Timeout: -1}, "")
		if err != nil {
			return fmt.Errorf("%s machine %s: %w", action, name, err)
		}
		if err := op.Wait(); err != nil {
			return fmt.Errorf("wait for machine %s %s: %w", name, action, err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported machine action %q", action)
	}
}

func v2MachineIPTimeout(vm bool) time.Duration {
	if vm {
		return 90 * time.Second // VM firmware + kernel boot before DHCP
	}
	return 45 * time.Second
}

func waitForV2InstanceIPv4(ctx context.Context, project TenantResourceServer, name string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		if state, _, err := project.GetInstanceState(name); err == nil {
			for _, network := range state.Network {
				if network.Type == "loopback" {
					continue
				}
				for _, address := range network.Addresses {
					if address.Family == "inet" && address.Scope == "global" {
						return address.Address
					}
				}
			}
		}
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			return ""
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(2 * time.Second):
		}
	}
}

// InstanceExists reports whether an instance exists in the given project —
// used by install preflights; connection or lookup errors read as "absent".
func (c TenantCreator) InstanceExists(project string, name string) bool {
	server, err := c.resolveV2Server()
	if err != nil {
		return false
	}
	_, _, err = server.UseProject(project).GetInstance(name)
	return err == nil
}
