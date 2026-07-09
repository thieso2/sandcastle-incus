package incusx

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	incus "github.com/lxc/incus/v6/client"
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
	// PrivateCIDR is the subnet the machine leased its address on, read from
	// the machine's own interface. A restricted tenant certificate cannot see
	// the tenant bridge's config, so this is the only authoritative source.
	PrivateCIDR string `json:"privateCIDR,omitempty"`
	LoginUser   string `json:"loginUser,omitempty"`
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
	if err := op.Wait(); err != nil && !isAlreadyRunning(err) {
		return CreateMachineV2Result{}, fmt.Errorf("wait for machine %s: %w", request.Name, err)
	}
	result.PrivateIP, result.PrivateCIDR = waitForV2InstanceIPv4(ctx, project, request.Name, v2MachineIPTimeout(request.VM))
	return result, nil
}

// EnsureMachineV2Result reports what EnsureMachineV2 had to do to make the
// machine reachable: created from scratch, started from stopped, or nothing.
type EnsureMachineV2Result struct {
	Name        string `json:"name"`
	Project     string `json:"incusProject"`
	Created     bool   `json:"created"`
	Started     bool   `json:"started"`
	PrivateIP   string `json:"privateIP,omitempty"`
	PrivateCIDR string `json:"privateCIDR,omitempty"`
	LoginUser   string `json:"loginUser"`
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
		result.PrivateIP, result.PrivateCIDR = waitForV2InstanceIPv4(ctx, project, request.Name, v2MachineIPTimeout(request.VM))
	case err == nil:
		result.PrivateIP, result.PrivateCIDR = waitForV2InstanceIPv4(ctx, project, request.Name, 20*time.Second)
	case api.StatusErrorCheck(err, http.StatusNotFound):
		created, err := c.CreateMachineV2(ctx, request)
		if err != nil {
			return EnsureMachineV2Result{}, err
		}
		result.Created = true
		result.PrivateIP, result.PrivateCIDR = created.PrivateIP, created.PrivateCIDR
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
	c.log("machine " + action + ": " + incusProject + "/" + name)
	instance, _, err := project.GetInstance(name)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return fmt.Errorf("machine %q not found in project %s", name, incusProject)
		}
		return fmt.Errorf("get machine %s: %w", name, err)
	}
	c.log("machine " + name + " is " + instance.Status)
	switch action {
	case "delete":
		if instance.StatusCode != api.Stopped {
			if err := c.runInstanceOp("force-stop "+name, func() (incus.Operation, error) {
				return project.UpdateInstanceState(name, api.InstanceStatePut{Action: "stop", Force: true}, "")
			}); err != nil {
				// A failed force-stop is not fatal: DeleteInstance below
				// reports the real reason if the instance is still running.
				c.log("force-stop " + name + " failed: " + err.Error())
			}
		}
		return c.runInstanceOp("delete "+name, func() (incus.Operation, error) {
			return project.DeleteInstance(name)
		})
	case "start", "stop", "restart":
		return c.runInstanceOp(action+" "+name, func() (incus.Operation, error) {
			return project.UpdateInstanceState(name, api.InstanceStatePut{Action: action, Force: action != "start", Timeout: -1}, "")
		})
	default:
		return fmt.Errorf("unsupported machine action %q", action)
	}
}

// runInstanceOp starts an Incus instance operation, waits for it, and reports
// its outcome and duration on the verbose log.
func (c TenantCreator) runInstanceOp(label string, start func() (incus.Operation, error)) error {
	began := time.Now()
	c.log("incus op: " + label + " started")
	op, err := start()
	if err != nil {
		c.log(fmt.Sprintf("incus op: %s failed (%s)", label, formatVerboseDuration(time.Since(began))))
		return fmt.Errorf("%s: %w", label, err)
	}
	if err := op.Wait(); err != nil {
		c.log(fmt.Sprintf("incus op: %s failed (%s)", label, formatVerboseDuration(time.Since(began))))
		return fmt.Errorf("wait for %s: %w", label, err)
	}
	c.log(fmt.Sprintf("incus op: %s done (%s)", label, formatVerboseDuration(time.Since(began))))
	return nil
}

func v2MachineIPTimeout(vm bool) time.Duration {
	if vm {
		return 90 * time.Second // VM firmware + kernel boot before DHCP
	}
	return 45 * time.Second
}

// waitForV2InstanceIPv4 returns the machine's global IPv4 address and the
// subnet it sits on, in CIDR form. Either may be empty: no lease yet, or an
// interface that did not report a usable netmask.
func waitForV2InstanceIPv4(ctx context.Context, project TenantResourceServer, name string, timeout time.Duration) (string, string) {
	deadline := time.Now().Add(timeout)
	for {
		if state, _, err := project.GetInstanceState(name); err == nil {
			for _, network := range state.Network {
				if network.Type == "loopback" {
					continue
				}
				for _, address := range network.Addresses {
					if address.Family == "inet" && address.Scope == "global" {
						return address.Address, subnetCIDR(address.Address, address.Netmask)
					}
				}
			}
		}
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			return "", ""
		}
		select {
		case <-ctx.Done():
			return "", ""
		case <-time.After(2 * time.Second):
		}
	}
}

// subnetCIDR turns an address plus Incus's netmask into the masked subnet.
// Incus reports a prefix length ("24") for bridged NICs, but some drivers
// report a dotted mask ("255.255.255.0"); both are accepted.
func subnetCIDR(address string, netmask string) string {
	addr, err := netip.ParseAddr(address)
	if err != nil || !addr.Is4() {
		return ""
	}
	bits, err := strconv.Atoi(strings.TrimSpace(netmask))
	if err != nil {
		mask, maskErr := netip.ParseAddr(strings.TrimSpace(netmask))
		if maskErr != nil || !mask.Is4() {
			return ""
		}
		bits = 0
		for _, octet := range mask.As4() {
			bits += bits8(octet)
		}
	}
	if bits <= 0 || bits > 32 {
		return ""
	}
	return netip.PrefixFrom(addr, bits).Masked().String()
}

func bits8(octet byte) int {
	count := 0
	for ; octet != 0; octet <<= 1 {
		count++
	}
	return count
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
