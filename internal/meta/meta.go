package meta

import (
	"encoding/json"
	"fmt"
	"strconv"
)

const (
	Prefix = "user.sandcastle."

	KeyKind        = Prefix + "kind"
	KeyVersion     = Prefix + "version"
	KeyTenant      = Prefix + "tenant"
	KeyProject     = Prefix + "project"
	KeyMachine     = Prefix + "machine"
	KeyType        = Prefix + "type"
	KeyHostname    = Prefix + "hostname"
	KeyPrivateCIDR = Prefix + "private_cidr"
	// KeyV2CIDR is where v2 stores a tenant's private /24, on the kind=infra
	// project (v1 keeps it in the tenant metadata under KeyPrivateCIDR). Must
	// match incusx's keyV2CIDR.
	KeyV2CIDR = Prefix + "v2.cidr"
	// KeyV2Suffix is where v2 stores the Tenant DNS Suffix, on the kind=infra
	// project (ADR-0018: tenant-chosen, defaults to the tenant name).
	KeyV2Suffix = Prefix + "v2.suffix"
	// KeyV2Prefix is where v2 stores the installation prefix, on the kind=infra
	// project — several sandcastles can share one Incus host (--prefix).
	KeyV2Prefix = Prefix + "v2.prefix"
	// KeyV2User is where v2 stores the profile login (Unix) user, on the
	// kind=infra project. It is the actual user@host for SSH — NOT the tenant
	// name. Must match incusx's keyV2User.
	KeyV2User    = Prefix + "v2.user"
	KeyAppPort   = Prefix + "app_port"
	KeyLinuxUser = Prefix + "linux_user"
	KeyCreatedBy = Prefix + "created_by"
	KeyState     = Prefix + "state"

	KindTenant    = "tenant"
	KindMachine   = "machine"
	KindRoute     = "route"
	KindSidecar   = "sidecar"
	KindInfra     = "infra"   // v2 per-tenant infra project (holds the sidecar + CIDR)
	KindV2Project = "project" // v2 per-project Incus project (app machines)

	Version = 1

	MachineTypeContainer = "container"

	TailscaleStateRunningLoggedOut = "running-logged-out"
)

type Tenant struct {
	Tenant        string               `json:"tenant"`
	Personal      bool                 `json:"personal,omitempty"`
	CreatedBy     string               `json:"createdBy,omitempty"`
	UnixUser      string               `json:"unixUser,omitempty"`
	Projects      []Project            `json:"projects"`
	StorageShares []TenantStorageShare `json:"storageShares,omitempty"`
	PrivateCIDR   string               `json:"privateCIDR"`
	SSHPublicKey  string               `json:"sshPublicKey,omitempty"`
	Tailscale     Tailscale            `json:"tailscale,omitempty"`
	Machines      []MachineRef         `json:"machines,omitempty"`
	PublicRoutes  []PublicRoute        `json:"publicRoutes,omitempty"`
}

type Project struct {
	Name            string `json:"name"`
	CreatedBy       string `json:"createdBy,omitempty"`
	CloudIdentity   string `json:"cloudIdentity,omitempty"`
	DockerAutostart bool   `json:"dockerAutostart,omitempty"`
}

type Tailscale struct {
	State            string   `json:"state,omitempty"`
	Tailnet          string   `json:"tailnet,omitempty"`
	Hostname         string   `json:"hostname,omitempty"`
	AdvertisedRoutes []string `json:"advertisedRoutes,omitempty"`
	TailscaleIPs     []string `json:"tailscaleIPs,omitempty"`
	LastCheckedAt    string   `json:"lastCheckedAt,omitempty"`
}

type MachineRef struct {
	Project string `json:"project"`
	Name    string `json:"name"`
	IP      string `json:"ip"`
}

type PublicRoute struct {
	Hostname  string `json:"hostname"`
	Project   string `json:"project"`
	Machine   string `json:"machine"`
	RoutePort int    `json:"routePort"`
}

type TenantStorageShare struct {
	SourceTenant  string                        `json:"sourceTenant"`
	SourceProject string                        `json:"sourceProject"`
	SourceDir     string                        `json:"sourceDir"`
	Name          string                        `json:"name"`
	Availability  string                        `json:"availability,omitempty"`
	CreatedBy     string                        `json:"createdBy,omitempty"`
	CreatedAt     string                        `json:"createdAt,omitempty"`
	Recipients    []TenantStorageShareRecipient `json:"recipients,omitempty"`
}

type TenantStorageShareRecipient struct {
	Tenant     string `json:"tenant"`
	State      string `json:"state"`
	OfferedBy  string `json:"offeredBy,omitempty"`
	OfferedAt  string `json:"offeredAt,omitempty"`
	AcceptedBy string `json:"acceptedBy,omitempty"`
	AcceptedAt string `json:"acceptedAt,omitempty"`
	DeclinedBy string `json:"declinedBy,omitempty"`
	DeclinedAt string `json:"declinedAt,omitempty"`
}

type Machine struct {
	Tenant          string   `json:"tenant"`
	Project         string   `json:"project"`
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	Template        string   `json:"template,omitempty"`
	AppPort         int      `json:"appPort"`
	PrivateIP       string   `json:"privateIP"`
	TailscaleIP     string   `json:"tailscaleIP,omitempty"`
	LinuxUser       string   `json:"linuxUser,omitempty"`
	CloudIdentity   string   `json:"cloudIdentity,omitempty"`
	DockerAutostart bool     `json:"dockerAutostart,omitempty"`
	HomeDir         string   `json:"homeDir,omitempty"`
	WorkspaceDir    string   `json:"workspaceDir,omitempty"`
	ContainerTools  bool     `json:"containerTools,omitempty"`
	ExtraSANs       []string `json:"extraSANs,omitempty"`
	CreatedBy       string   `json:"createdBy,omitempty"`
	CreatedAt       string   `json:"createdAt,omitempty"`
	Running         bool     `json:"running,omitempty"`
}

type Route struct {
	Hostname        string `json:"hostname"`
	TargetTenant    string `json:"targetTenant"`
	TargetProject   string `json:"targetProject"`
	TargetMachine   string `json:"targetMachine"`
	TargetIP        string `json:"targetIP"`
	RoutePort       int    `json:"routePort"`
	CreatedBy       string `json:"createdBy,omitempty"`
	IngressAttached bool   `json:"ingressAttached,omitempty"`
}

func TenantConfig(tenant Tenant) (map[string]string, error) {
	state, err := encodeState(tenant)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		KeyKind:        KindTenant,
		KeyVersion:     strconv.Itoa(Version),
		KeyTenant:      tenant.Tenant,
		KeyPrivateCIDR: tenant.PrivateCIDR,
		KeyState:       state,
	}, nil
}

func ParseTenantConfig(config map[string]string) (Tenant, error) {
	if err := requireKind(config, KindTenant); err != nil {
		return Tenant{}, err
	}
	var tenant Tenant
	if err := decodeState(config[KeyState], &tenant); err != nil {
		return Tenant{}, err
	}
	return tenant, nil
}

func MachineConfig(machine Machine) (map[string]string, error) {
	state, err := encodeState(machine)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		KeyKind:      KindMachine,
		KeyVersion:   strconv.Itoa(Version),
		KeyTenant:    machine.Tenant,
		KeyProject:   machine.Project,
		KeyMachine:   machine.Name,
		KeyType:      machine.Type,
		KeyAppPort:   strconv.Itoa(machine.AppPort),
		KeyLinuxUser: machine.LinuxUser,
		KeyCreatedBy: machine.CreatedBy,
		KeyState:     state,
	}, nil
}

func ParseMachineConfig(config map[string]string) (Machine, error) {
	if err := requireKind(config, KindMachine); err != nil {
		return Machine{}, err
	}
	var machine Machine
	if err := decodeState(config[KeyState], &machine); err != nil {
		return Machine{}, err
	}
	if machine.Type == "" && config[KeyType] != "" {
		machine.Type = config[KeyType]
	}
	if machine.Type == "" {
		machine.Type = MachineTypeContainer
	}
	if machine.LinuxUser == "" && config[KeyLinuxUser] != "" {
		machine.LinuxUser = config[KeyLinuxUser]
	}
	if machine.LinuxUser == "" {
		machine.LinuxUser = machine.CreatedBy
	}
	return machine, nil
}

func RouteConfig(route Route) (map[string]string, error) {
	state, err := encodeState(route)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		KeyKind:      KindRoute,
		KeyVersion:   strconv.Itoa(Version),
		KeyHostname:  route.Hostname,
		KeyTenant:    route.TargetTenant,
		KeyProject:   route.TargetProject,
		KeyMachine:   route.TargetMachine,
		KeyAppPort:   strconv.Itoa(route.RoutePort),
		KeyCreatedBy: route.CreatedBy,
		KeyState:     state,
	}, nil
}

func ParseRouteConfig(config map[string]string) (Route, error) {
	if err := requireKind(config, KindRoute); err != nil {
		return Route{}, err
	}
	var route Route
	if err := decodeState(config[KeyState], &route); err != nil {
		return Route{}, err
	}
	return route, nil
}

func IsManaged(config map[string]string) bool {
	return config[KeyKind] != "" && config[KeyVersion] != ""
}

func requireKind(config map[string]string, kind string) error {
	if config[KeyKind] != kind {
		return fmt.Errorf("metadata kind = %q, want %q", config[KeyKind], kind)
	}
	if config[KeyVersion] != strconv.Itoa(Version) {
		return fmt.Errorf("metadata version = %q, want %d", config[KeyVersion], Version)
	}
	return nil
}

func encodeState(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeState(value string, target any) error {
	if value == "" {
		return fmt.Errorf("metadata state is required")
	}
	return json.Unmarshal([]byte(value), target)
}
