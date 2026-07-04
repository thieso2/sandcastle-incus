package incusx

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/lxc/incus/v6/shared/api"
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
		Name:    request.Name,
		Type:    string(instanceType),
		Project: request.IncusProject,
		Image:   request.Image,
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
