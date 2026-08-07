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
	// HomeShare adds the project's `homeshare` profile beside default, so the
	// machine mounts the shared /home volume instead of a machine-local /home.
	// Profiles are applied at create time only — an existing machine keeps the
	// profiles it was created with.
	HomeShare bool
	// Bare overrides the profile's cloud-init user-data at the INSTANCE level
	// with tenant.V2BareUserData: correct hostname and a Caddy serving the
	// tenant-CA leaf, but no login user, no SSH key and no sshd. The profile
	// still applies, so the machine keeps its NIC, root disk, /workspace and
	// /.sc — only the cloud-init half is replaced.
	Bare bool
}

// v2MachineProfiles returns the profile list a machine is created with:
// default always, plus the opt-in homeshare profile.
func v2MachineProfiles(homeShare bool) []string {
	if homeShare {
		return []string{"default", tenant.V2HomeShareProfileName}
	}
	return []string{"default"}
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
	// HomeShare reports whether the machine was created with the homeshare
	// profile (shared /home) — /workspace is shared either way.
	HomeShare bool `json:"homeShare,omitempty"`
	// Bare reports whether the machine was created with the bare cloud-init
	// (no login user, no sshd). Callers use it to skip the SSH advice.
	Bare bool `json:"bare,omitempty"`
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
		HomeShare: request.HomeShare,
		Bare:      request.Bare,
	}
	// A bare machine has no login user to report — and the profile read that
	// would find one is spent on its cloud-init override instead.
	var instanceConfig api.ConfigMap
	var instanceDevices api.DevicesMap
	if request.Bare {
		instanceConfig, err = v2BareInstanceConfig(project, request.IncusProject)
		if err != nil {
			return CreateMachineV2Result{}, err
		}
		instanceDevices = v2BareInstanceDevices()
	} else {
		result.LoginUser = v2ProfileLoginUser(project)
	}
	c.log("launching " + result.Type + " " + request.Name + " from " + request.Image + " into " + request.IncusProject)
	op, err := project.CreateInstance(api.InstancesPost{
		Name:   request.Name,
		Type:   instanceType,
		Start:  true,
		Source: imageInstanceSource(request.Image),
		InstancePut: api.InstancePut{
			Profiles: v2MachineProfiles(request.HomeShare),
			Config:   instanceConfig,
			Devices:  instanceDevices,
		},
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

// A bare machine's identity is read BACK off the project's default profile
// rather than derived from the tenant summary: a restricted tenant certificate
// cannot see the kind=infra project, so summary.DNSAddress (the leaf signer) is
// empty for exactly the callers that run `sc create`. The profile is the one
// source both the CLI and the broker can always reach, and reusing it means a
// bare machine and its siblings can never disagree about the tenant's zone.
var (
	v2ProfileFQDNPattern   = regexp.MustCompile(`(?m)^fqdn:\s*\{\{\s*v1\.local_hostname\s*\}\}\.(\S+?)\s*$`)
	v2ProfileSignerPattern = regexp.MustCompile(`(?m)^\s*SIGNER=(\S+)\s*$`)
)

// v2BareInstanceConfig builds the instance-level config of a `--bare` machine:
// the bare cloud-init, rendered from the identity its project's default profile
// already carries.
//
// A profile without that identity is a hard error rather than a boot without
// certificates: --bare promises exactly two things, and a machine that silently
// came up with neither a leaf nor a way to log in and fix it is worse than one
// that was never created.
func v2BareInstanceConfig(project TenantResourceServer, incusProject string) (api.ConfigMap, error) {
	profile, _, err := project.GetProfile("default")
	if err != nil {
		return nil, fmt.Errorf("read the default profile of %s for --bare: %w", incusProject, err)
	}
	userData := profile.Config["cloud-init.user-data"]
	domain := firstSubmatch(v2ProfileFQDNPattern, userData)
	signer := firstSubmatch(v2ProfileSignerPattern, userData)
	if domain == "" || signer == "" {
		return nil, fmt.Errorf("project %s cannot host a --bare machine: its default profile carries no machine FQDN and TLS signer, "+
			"so the machine would boot with no certificate and no way in — re-provision the project (sc project create %s) to re-render it",
			incusProject, shortProjectName(incusProject))
	}
	return api.ConfigMap{"cloud-init.user-data": tenant.V2BareUserData(domain, signer)}, nil
}

// v2BareInstanceDevices masks the project's SHARED filesystems on a bare
// machine. `type: none` is Incus's device-inhibit type: an instance device of
// that name shadows the profile's, so the mount never happens — the same
// instance-beats-profile rule that carries the bare cloud-init, rather than a
// third profile the project would have to be re-provisioned to gain.
//
// Masked by name, not by category, and deliberately NOT sc-platform: the bare
// machine's boot shims source /.sc/platform/sbin/caddy-setup, so masking that
// one would leave it with no Caddy at all. A device added to the default
// profile later is inherited by bare machines until it is named here.
//
// This also settles the homeshare migration: appending the `homeshare` profile
// to a bare machine is a no-op, because the instance-level mask outranks it.
func v2BareInstanceDevices() api.DevicesMap {
	return api.DevicesMap{
		"home":      {"type": "none"},
		"workspace": {"type": "none"},
	}
}

func firstSubmatch(pattern *regexp.Regexp, value string) string {
	if match := pattern.FindStringSubmatch(value); match != nil {
		return strings.TrimSpace(match[1])
	}
	return ""
}

// shortProjectName is the trailing segment of <prefix>-<tenant>-<project>: what
// the tenant calls the project, for use in advice they can paste back.
func shortProjectName(incusProject string) string {
	if idx := strings.LastIndex(incusProject, "-"); idx >= 0 {
		return incusProject[idx+1:]
	}
	return incusProject
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

// waitForV2InstanceIPv4 returns the machine's global IPv4 address and the
// subnet it sits on, in CIDR form. Either may be empty: no lease yet, or an
// interface that did not report a usable netmask.
func waitForV2InstanceIPv4(ctx context.Context, project TenantResourceServer, name string, timeout time.Duration) (string, string) {
	deadline := time.Now().Add(timeout)
	for {
		// Devices are re-read each pass: the instance may not exist yet on the
		// first iteration, and a NIC can be hot-plugged while we wait.
		instance, _, instanceErr := project.GetInstance(name)
		if state, _, err := project.GetInstanceState(name); err == nil && instanceErr == nil && instance != nil {
			if nic := instanceNICIPv4(instance.ExpandedConfig, instance.ExpandedDevices, state.Network); nic.Address != "" {
				return nic.Address, subnetCIDR(nic.Address, nic.Netmask)
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
