package infra

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/caddy"
	"github.com/thieso2/sandcastle-incus/internal/certs"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

const (
	RouteBrokerName            = "sc-route-broker"
	RouteBrokerListen          = ":9443"
	RouteBrokerBinaryPath      = "/usr/local/bin/sandcastle-admin"
	RouteBrokerCertPath        = "/etc/sandcastle/route-broker/tls.crt"
	RouteBrokerKeyPath         = "/etc/sandcastle/route-broker/tls.key"
	RouteBrokerEnvPath         = "/etc/sandcastle/route-broker/env"
	RouteBrokerUnitPath        = "/etc/systemd/system/sandcastle-route-broker.service"
	RouteBrokerIncusSocketPath = "/var/lib/incus/unix.socket"
	AuthAppName                = "sc-auth-app"
	AuthAppListen              = ":9444"
	AuthAppBinaryPath          = "/usr/local/bin/sandcastle-admin"
	AuthAppDatabasePath        = "/var/lib/sandcastle/auth/auth.db"
	AuthAppEnvPath             = "/etc/sandcastle/auth-app/env"
	AuthAppUnitPath            = "/etc/systemd/system/sandcastle-auth-app.service"
	InfrastructureNetworkName  = "incusbr0"
	NetworkdEth0Path           = "/etc/systemd/network/10-eth0.network"
	StaticNetworkScriptPath    = "/usr/local/sbin/sandcastle-infra-network"
	StaticNetworkUnitPath      = "/etc/systemd/system/sandcastle-infra-network.service"
)

type CreateRequest struct{}

type DeleteRequest struct {
	Project string
}

type CreatePlan struct {
	Project               string             `json:"project"`
	StoragePool           string             `json:"storagePool"`
	CaddyInstance         string             `json:"caddyInstance"`
	RouteBrokerInstance   string             `json:"routeBrokerInstance"`
	AuthAppInstance       string             `json:"authAppInstance"`
	ProjectMetadataConfig map[string]string  `json:"projectMetadataConfig"`
	Instances             []InstancePlan     `json:"instances"`
	RuntimeDirectories    []RuntimeDirectory `json:"runtimeDirectories"`
	RuntimeFiles          []RuntimeFile      `json:"runtimeFiles"`
	RuntimeBinaries       []RuntimeBinary    `json:"runtimeBinaries"`
	RuntimeCommands       []RuntimeCommand   `json:"runtimeCommands"`
}

type InstancePlan struct {
	Name       string            `json:"name"`
	Role       string            `json:"role"`
	ImageAlias string            `json:"imageAlias"`
	Config     map[string]string `json:"config"`
	Devices    map[string]Device `json:"devices"`
	Start      bool              `json:"start"`
}

type Device map[string]string

type RuntimeDirectory struct {
	Instance string `json:"instance"`
	Path     string `json:"path"`
	Mode     int    `json:"mode"`
}

type RuntimeFile struct {
	Instance string `json:"instance"`
	Path     string `json:"path"`
	Content  string `json:"content"`
	Mode     int    `json:"mode"`
}

type RuntimeBinary struct {
	Instance   string `json:"instance"`
	SourcePath string `json:"sourcePath"`
	TargetPath string `json:"targetPath"`
	Mode       int    `json:"mode"`
}

type RuntimeCommand struct {
	Instance    string   `json:"instance"`
	Description string   `json:"description"`
	Command     []string `json:"command"`
}

type StaticNetwork struct {
	Gateway      string            `json:"gateway"`
	PrefixLength int               `json:"prefixLength"`
	Addresses    map[string]string `json:"addresses"`
}

type Creator interface {
	CreateInfrastructure(context.Context, CreatePlan) error
}

type DeletePlan struct {
	Project          string   `json:"project"`
	RuntimeInstances []string `json:"runtimeInstances"`
}

type Deleter interface {
	DeleteInfrastructure(context.Context, DeletePlan) error
}

func PlanCreate(admin config.Admin, request CreateRequest) (CreatePlan, error) {
	if err := admin.Validate(); err != nil {
		return CreatePlan{}, err
	}
	project := strings.TrimSpace(admin.InfrastructureProject)
	if project == "" {
		return CreatePlan{}, fmt.Errorf("infrastructure project is required")
	}
	projectConfig := map[string]string{
		meta.KeyKind:         "infrastructure",
		meta.KeyVersion:      "1",
		meta.Prefix + "name": project,
		"features.images":    "false",
	}
	brokerTLS, err := certs.GenerateSelfSignedServer("Sandcastle route broker", []string{RouteBrokerName, "localhost"}, time.Now().UTC())
	if err != nil {
		return CreatePlan{}, err
	}
	return CreatePlan{
		Project:               project,
		StoragePool:           admin.StoragePool,
		CaddyInstance:         route.InfrastructureCaddyName,
		RouteBrokerInstance:   RouteBrokerName,
		AuthAppInstance:       AuthAppName,
		ProjectMetadataConfig: projectConfig,
		Instances: []InstancePlan{
			instancePlan(admin, route.InfrastructureCaddyName, "caddy"),
			instancePlan(admin, RouteBrokerName, "route-broker"),
			instancePlan(admin, AuthAppName, "auth-app"),
		},
		RuntimeDirectories: runtimeDirectories(),
		RuntimeFiles:       runtimeFiles(admin, brokerTLS),
		RuntimeBinaries:    runtimeBinaries(),
		RuntimeCommands:    runtimeCommands(),
	}, nil
}

func runtimeDirectories() []RuntimeDirectory {
	return []RuntimeDirectory{
		{Instance: route.InfrastructureCaddyName, Path: "/etc/caddy", Mode: 0o755},
		{Instance: RouteBrokerName, Path: "/etc/sandcastle", Mode: 0o755},
		{Instance: RouteBrokerName, Path: "/etc/sandcastle/route-broker", Mode: 0o700},
		{Instance: AuthAppName, Path: "/etc/sandcastle", Mode: 0o755},
		{Instance: AuthAppName, Path: "/etc/sandcastle/auth-app", Mode: 0o700},
		{Instance: AuthAppName, Path: "/var/lib/sandcastle/auth", Mode: 0o700},
	}
}

func runtimeFiles(admin config.Admin, brokerTLS certs.KeyPair) []RuntimeFile {
	caddyFile := caddy.RenderInfrastructureWithOptions(nil, caddy.InfrastructureOptions{
		LetsEncryptEmail: admin.LetsEncryptEmail,
		AuthHostname:     admin.AuthHostname,
		AuthUpstream:     "http://" + AuthAppName + AuthAppListen,
	})
	files := networkRuntimeFiles([]string{route.InfrastructureCaddyName, RouteBrokerName, AuthAppName})
	files = append(files,
		RuntimeFile{
			Instance: route.InfrastructureCaddyName,
			Path:     caddyFile.Path,
			Content:  caddyFile.Content,
			Mode:     caddyFile.Mode,
		},
		RuntimeFile{
			Instance: RouteBrokerName,
			Path:     RouteBrokerEnvPath,
			Content: strings.Join([]string{
				envLine("SANDCASTLE_ROUTE_BROKER_LISTEN", RouteBrokerListen),
				envLine("SANDCASTLE_ROUTE_BROKER_CERT", RouteBrokerCertPath),
				envLine("SANDCASTLE_ROUTE_BROKER_KEY", RouteBrokerKeyPath),
				envLine("SANDCASTLE_REMOTE", admin.Remote),
				envLine("SANDCASTLE_STORAGE_POOL", admin.StoragePool),
				envLine("SANDCASTLE_CIDR_POOL", admin.CIDRPool),
				envLine("SANDCASTLE_INCUS_PROJECT_PREFIX", admin.IncusProjectPrefix),
				envLine("SANDCASTLE_INFRA_PROJECT", admin.InfrastructureProject),
				envLine("SANDCASTLE_INFRA_HOST", admin.InfrastructureHost),
				envLine("SANDCASTLE_LETSENCRYPT_EMAIL", admin.LetsEncryptEmail),
				envLine("SANDCASTLE_BASE_IMAGE", admin.Images.Base),
				envLine("SANDCASTLE_AI_IMAGE", admin.Images.AI),
				"",
			}, "\n"),
			Mode: 0o600,
		},
		RuntimeFile{
			Instance: RouteBrokerName,
			Path:     RouteBrokerCertPath,
			Content:  string(brokerTLS.CertificatePEM),
			Mode:     0o644,
		},
		RuntimeFile{
			Instance: RouteBrokerName,
			Path:     RouteBrokerKeyPath,
			Content:  string(brokerTLS.PrivateKeyPEM),
			Mode:     0o600,
		},
		RuntimeFile{
			Instance: RouteBrokerName,
			Path:     RouteBrokerUnitPath,
			Content:  "[Unit]\nDescription=Sandcastle route broker\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nEnvironmentFile=" + RouteBrokerEnvPath + "\nExecStart=" + RouteBrokerBinaryPath + " route-broker serve --listen ${SANDCASTLE_ROUTE_BROKER_LISTEN} --cert ${SANDCASTLE_ROUTE_BROKER_CERT} --key ${SANDCASTLE_ROUTE_BROKER_KEY}\nRestart=on-failure\n\n[Install]\nWantedBy=multi-user.target\n",
			Mode:     0o644,
		},
		RuntimeFile{
			Instance: AuthAppName,
			Path:     AuthAppEnvPath,
			Content: strings.Join([]string{
				envLine("SANDCASTLE_AUTH_LISTEN", AuthAppListen),
				envLine("SANDCASTLE_AUTH_DB", AuthAppDatabasePath),
				envLine("SANDCASTLE_AUTH_HOSTNAME", admin.AuthHostname),
				envLine("SANDCASTLE_AUTH_GITHUB_CLIENT_ID", admin.AuthGitHubClientID),
				envLine("SANDCASTLE_AUTH_GITHUB_CLIENT_SECRET", admin.AuthGitHubClientSecret),
				envLine("SANDCASTLE_AUTH_ADMIN_GITHUB_USERS", strings.Join(admin.AuthAdminGitHubUsers, ",")),
				envLine("SANDCASTLE_REMOTE", admin.Remote),
				envLine("SANDCASTLE_STORAGE_POOL", admin.StoragePool),
				envLine("SANDCASTLE_CIDR_POOL", admin.CIDRPool),
				envLine("SANDCASTLE_INCUS_PROJECT_PREFIX", admin.IncusProjectPrefix),
				envLine("SANDCASTLE_INFRA_PROJECT", admin.InfrastructureProject),
				envLine("SANDCASTLE_BASE_IMAGE", admin.Images.Base),
				envLine("SANDCASTLE_AI_IMAGE", admin.Images.AI),
				"",
			}, "\n"),
			Mode: 0o600,
		},
		RuntimeFile{
			Instance: AuthAppName,
			Path:     AuthAppUnitPath,
			Content:  "[Unit]\nDescription=Sandcastle auth app\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nEnvironmentFile=" + AuthAppEnvPath + "\nExecStart=" + AuthAppBinaryPath + " auth-app serve --listen ${SANDCASTLE_AUTH_LISTEN} --database ${SANDCASTLE_AUTH_DB} --auth-hostname ${SANDCASTLE_AUTH_HOSTNAME} --github-client-id ${SANDCASTLE_AUTH_GITHUB_CLIENT_ID} --github-client-secret ${SANDCASTLE_AUTH_GITHUB_CLIENT_SECRET} --admin-github-users ${SANDCASTLE_AUTH_ADMIN_GITHUB_USERS}\nRestart=on-failure\n\n[Install]\nWantedBy=multi-user.target\n",
			Mode:     0o644,
		},
	)
	return files
}

func networkRuntimeFiles(instances []string) []RuntimeFile {
	content := strings.Join([]string{
		"[Match]",
		"Name=eth0",
		"",
		"[Network]",
		"DHCP=yes",
		"IPv6AcceptRA=yes",
		"",
	}, "\n")
	files := make([]RuntimeFile, 0, len(instances))
	for _, instance := range instances {
		files = append(files, RuntimeFile{
			Instance: instance,
			Path:     NetworkdEth0Path,
			Content:  content,
			Mode:     0o644,
		})
	}
	return files
}

func networkBootstrapShell() []string {
	return []string{
		"systemctl enable systemd-networkd",
		"systemctl restart systemd-networkd",
		"networkctl renew eth0 || true",
		"for i in $(seq 1 50); do ip -4 addr show dev eth0 | grep -q 'inet ' && break; sleep 0.1; done",
		"ip -4 addr show dev eth0 | grep -q 'inet '",
	}
}

func ApplyStaticNetwork(plan CreatePlan, network StaticNetwork) CreatePlan {
	plan.RuntimeFiles = withoutRuntimePath(plan.RuntimeFiles, NetworkdEth0Path)
	for _, instance := range plan.Instances {
		address := strings.TrimSpace(network.Addresses[instance.Name])
		if address == "" {
			continue
		}
		plan.RuntimeFiles = append(plan.RuntimeFiles,
			RuntimeFile{
				Instance: instance.Name,
				Path:     StaticNetworkScriptPath,
				Content:  staticNetworkScript(address, network.PrefixLength, network.Gateway),
				Mode:     0o755,
			},
			RuntimeFile{
				Instance: instance.Name,
				Path:     StaticNetworkUnitPath,
				Content:  staticNetworkUnit(),
				Mode:     0o644,
			},
		)
	}
	if authAddress := strings.TrimSpace(network.Addresses[AuthAppName]); authAddress != "" {
		replaceRuntimeFileContent(&plan, route.InfrastructureCaddyName, "/etc/caddy/Caddyfile", func(content string) string {
			return strings.ReplaceAll(content, "http://"+AuthAppName+AuthAppListen, "http://"+authAddress+AuthAppListen)
		})
	}
	oldBootstrap := strings.Join(networkBootstrapShell(), "; ")
	newBootstrap := strings.Join(staticNetworkBootstrapShell(), "; ")
	for i := range plan.RuntimeCommands {
		if len(plan.RuntimeCommands[i].Command) >= 3 {
			plan.RuntimeCommands[i].Command[2] = strings.Replace(plan.RuntimeCommands[i].Command[2], oldBootstrap, newBootstrap, 1)
		}
	}
	return plan
}

func withoutRuntimePath(files []RuntimeFile, path string) []RuntimeFile {
	output := files[:0]
	for _, file := range files {
		if file.Path != path {
			output = append(output, file)
		}
	}
	return output
}

func replaceRuntimeFileContent(plan *CreatePlan, instance string, path string, replace func(string) string) {
	for i := range plan.RuntimeFiles {
		if plan.RuntimeFiles[i].Instance == instance && plan.RuntimeFiles[i].Path == path {
			plan.RuntimeFiles[i].Content = replace(plan.RuntimeFiles[i].Content)
		}
	}
}

func staticNetworkScript(address string, prefixLength int, gateway string) string {
	return strings.Join([]string{
		"#!/bin/sh",
		"set -eu",
		"/usr/sbin/ip link set eth0 up",
		fmt.Sprintf("/usr/sbin/ip addr replace %s/%d dev eth0", address, prefixLength),
		"/usr/sbin/ip route replace default via " + gateway,
		"",
	}, "\n")
}

func staticNetworkUnit() string {
	return strings.Join([]string{
		"[Unit]",
		"Description=Sandcastle infrastructure static network",
		"After=network-pre.target",
		"Before=network-online.target",
		"",
		"[Service]",
		"Type=oneshot",
		"ExecStart=" + StaticNetworkScriptPath,
		"RemainAfterExit=yes",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}, "\n")
}

func staticNetworkBootstrapShell() []string {
	return []string{
		"systemctl daemon-reload",
		"systemctl enable sandcastle-infra-network.service",
		"systemctl restart sandcastle-infra-network.service",
		"ip -4 addr show dev eth0 | grep -q 'inet '",
	}
}

func envLine(key string, value string) string {
	return key + "=" + shellQuote(value)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func runtimeBinaries() []RuntimeBinary {
	source := strings.TrimSpace(os.Getenv("SANDCASTLE_ADMIN_BIN"))
	if source == "" {
		source = strings.TrimSpace(os.Getenv("SANDCASTLE_BIN"))
	}
	if source == "" {
		if executable, err := os.Executable(); err == nil {
			source = executable
		}
	}
	if source == "" {
		return nil
	}
	return []RuntimeBinary{
		{Instance: RouteBrokerName, SourcePath: source, TargetPath: RouteBrokerBinaryPath, Mode: 0o755},
		{Instance: AuthAppName, SourcePath: source, TargetPath: AuthAppBinaryPath, Mode: 0o755},
	}
}

func runtimeCommands() []RuntimeCommand {
	return []RuntimeCommand{
		{
			Instance:    route.InfrastructureCaddyName,
			Description: "start infrastructure Caddy",
			Command: []string{"/bin/sh", "-lc", strings.Join(append(networkBootstrapShell(),
				"install -d /etc/caddy",
				"systemctl restart caddy",
				"for i in $(seq 1 50); do systemctl is-active caddy >/dev/null 2>&1 && exit 0; sleep 0.1; done",
				"systemctl is-active caddy",
			), "; ")},
		},
		{
			Instance:    RouteBrokerName,
			Description: "start route broker service",
			Command: []string{"/bin/sh", "-lc", strings.Join(append(networkBootstrapShell(),
				"systemctl daemon-reload",
				"systemctl enable sandcastle-route-broker",
				"systemctl restart sandcastle-route-broker",
				"for i in $(seq 1 50); do systemctl is-active sandcastle-route-broker >/dev/null 2>&1 && exit 0; sleep 0.1; done",
				"systemctl is-active sandcastle-route-broker",
			), "; ")},
		},
		{
			Instance:    AuthAppName,
			Description: "start auth app service",
			Command: []string{"/bin/sh", "-lc", strings.Join(append(networkBootstrapShell(),
				"systemctl daemon-reload",
				"systemctl enable sandcastle-auth-app",
				"systemctl restart sandcastle-auth-app",
				"for i in $(seq 1 50); do systemctl is-active sandcastle-auth-app >/dev/null 2>&1 && exit 0; sleep 0.1; done",
				"systemctl is-active sandcastle-auth-app",
			), "; ")},
		},
	}
}

func instancePlan(admin config.Admin, name string, role string) InstancePlan {
	instanceConfig := map[string]string{
		meta.KeyKind:         "infrastructure",
		meta.KeyVersion:      "1",
		meta.Prefix + "name": name,
		meta.Prefix + "role": role,
	}
	devices := map[string]Device{
		"root": {
			"type": "disk",
			"pool": admin.StoragePool,
			"path": "/",
		},
		"eth0": {
			"type":    "nic",
			"nictype": "bridged",
			"parent":  InfrastructureNetworkName,
		},
	}
	if (name == RouteBrokerName || name == AuthAppName) && strings.TrimSpace(admin.RouteBrokerIncusSocket) != "" {
		devices["incus-socket"] = Device{
			"type":   "disk",
			"source": strings.TrimSpace(admin.RouteBrokerIncusSocket),
			"path":   RouteBrokerIncusSocketPath,
		}
		instanceConfig["security.privileged"] = "true"
	}
	return InstancePlan{
		Name:       name,
		Role:       role,
		ImageAlias: admin.Images.Base,
		Config:     instanceConfig,
		Devices:    devices,
		Start:      true,
	}
}

func PlanDelete(admin config.Admin, request DeleteRequest) (DeletePlan, error) {
	if err := admin.Validate(); err != nil {
		return DeletePlan{}, err
	}
	project := strings.TrimSpace(request.Project)
	if project == "" {
		project = strings.TrimSpace(admin.InfrastructureProject)
	}
	if project == "" {
		return DeletePlan{}, fmt.Errorf("infrastructure project is required")
	}
	if err := naming.ValidateIncusProjectName(project); err != nil {
		return DeletePlan{}, err
	}
	return DeletePlan{
		Project:          project,
		RuntimeInstances: []string{route.InfrastructureCaddyName, RouteBrokerName, AuthAppName},
	}, nil
}
