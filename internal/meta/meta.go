package meta

import (
	"encoding/json"
	"fmt"
	"strconv"
)

const (
	Prefix = "user.sandcastle."

	KeyKind            = Prefix + "kind"
	KeyVersion         = Prefix + "version"
	KeyOwner           = Prefix + "owner"
	KeyProject         = Prefix + "project"
	KeyDomain          = Prefix + "domain"
	KeyPrivateCIDR     = Prefix + "private_cidr"
	KeyDefaultTemplate = Prefix + "default_template"
	KeyName            = Prefix + "name"
	KeyHostname        = Prefix + "hostname"
	KeyAppPort         = Prefix + "app_port"
	KeyState           = Prefix + "state"

	KindProject = "project"
	KindSandbox = "sandbox"
	KindRoute   = "route"

	Version = 1
)

type Project struct {
	Owner           string       `json:"owner"`
	Project         string       `json:"project"`
	Domain          string       `json:"domain"`
	PrivateCIDR     string       `json:"privateCIDR"`
	DefaultTemplate string       `json:"defaultTemplate"`
	Tailscale       Tailscale    `json:"tailscale,omitempty"`
	Sandboxes       []SandboxRef `json:"sandboxes,omitempty"`
}

type Tailscale struct {
	State            string   `json:"state,omitempty"`
	Tailnet          string   `json:"tailnet,omitempty"`
	Hostname         string   `json:"hostname,omitempty"`
	AdvertisedRoutes []string `json:"advertisedRoutes,omitempty"`
	TailscaleIPs     []string `json:"tailscaleIPs,omitempty"`
	LastCheckedAt    string   `json:"lastCheckedAt,omitempty"`
}

type SandboxRef struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
}

type Sandbox struct {
	Owner        string   `json:"owner"`
	Project      string   `json:"project"`
	Name         string   `json:"name"`
	AppPort      int      `json:"appPort"`
	PrivateIP    string   `json:"privateIP"`
	HomeDir      string   `json:"homeDir,omitempty"`
	WorkspaceDir string   `json:"workspaceDir,omitempty"`
	ExtraSANs    []string `json:"extraSANs,omitempty"`
}

type Route struct {
	Hostname        string `json:"hostname"`
	TargetOwner     string `json:"targetOwner"`
	TargetProject   string `json:"targetProject"`
	TargetSandbox   string `json:"targetSandbox"`
	TargetIP        string `json:"targetIP"`
	RoutePort       int    `json:"routePort"`
	CreatedBy       string `json:"createdBy,omitempty"`
	IngressAttached bool   `json:"ingressAttached,omitempty"`
}

func ProjectConfig(project Project) (map[string]string, error) {
	state, err := encodeState(project)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		KeyKind:            KindProject,
		KeyVersion:         strconv.Itoa(Version),
		KeyOwner:           project.Owner,
		KeyProject:         project.Project,
		KeyDomain:          project.Domain,
		KeyPrivateCIDR:     project.PrivateCIDR,
		KeyDefaultTemplate: project.DefaultTemplate,
		KeyState:           state,
	}, nil
}

func ParseProjectConfig(config map[string]string) (Project, error) {
	if err := requireKind(config, KindProject); err != nil {
		return Project{}, err
	}
	var project Project
	if err := decodeState(config[KeyState], &project); err != nil {
		return Project{}, err
	}
	return project, nil
}

func SandboxConfig(sandbox Sandbox) (map[string]string, error) {
	state, err := encodeState(sandbox)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		KeyKind:    KindSandbox,
		KeyVersion: strconv.Itoa(Version),
		KeyOwner:   sandbox.Owner,
		KeyProject: sandbox.Project,
		KeyName:    sandbox.Name,
		KeyAppPort: strconv.Itoa(sandbox.AppPort),
		KeyState:   state,
	}, nil
}

func ParseSandboxConfig(config map[string]string) (Sandbox, error) {
	if err := requireKind(config, KindSandbox); err != nil {
		return Sandbox{}, err
	}
	var sandbox Sandbox
	if err := decodeState(config[KeyState], &sandbox); err != nil {
		return Sandbox{}, err
	}
	return sandbox, nil
}

func RouteConfig(route Route) (map[string]string, error) {
	state, err := encodeState(route)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		KeyKind:     KindRoute,
		KeyVersion:  strconv.Itoa(Version),
		KeyHostname: route.Hostname,
		KeyOwner:    route.TargetOwner,
		KeyProject:  route.TargetProject,
		KeyName:     route.TargetSandbox,
		KeyAppPort:  strconv.Itoa(route.RoutePort),
		KeyState:    state,
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
