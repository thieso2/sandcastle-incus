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
	RouteBrokerBinaryPath      = "/usr/local/bin/sandcastle"
	RouteBrokerCertPath        = "/etc/sandcastle/route-broker/tls.crt"
	RouteBrokerKeyPath         = "/etc/sandcastle/route-broker/tls.key"
	RouteBrokerEnvPath         = "/etc/sandcastle/route-broker/env"
	RouteBrokerUnitPath        = "/etc/systemd/system/sandcastle-route-broker.service"
	RouteBrokerIncusSocketPath = "/var/lib/incus/unix.socket"
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
		meta.KeyKind:        "infrastructure",
		meta.KeyVersion:     "1",
		meta.KeyName:        project,
		"features.images":   "false",
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
		ProjectMetadataConfig: projectConfig,
		Instances: []InstancePlan{
			instancePlan(admin, route.InfrastructureCaddyName, "caddy"),
			instancePlan(admin, RouteBrokerName, "route-broker"),
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
	}
}

func runtimeFiles(admin config.Admin, brokerTLS certs.KeyPair) []RuntimeFile {
	caddyFile := caddy.RenderInfrastructureWithOptions(nil, caddy.InfrastructureOptions{LetsEncryptEmail: admin.LetsEncryptEmail})
	return []RuntimeFile{
		{
			Instance: route.InfrastructureCaddyName,
			Path:     caddyFile.Path,
			Content:  caddyFile.Content,
			Mode:     caddyFile.Mode,
		},
		{
			Instance: RouteBrokerName,
			Path:     RouteBrokerEnvPath,
			Content: strings.Join([]string{
				envLine("SANDCASTLE_ROUTE_BROKER_LISTEN", RouteBrokerListen),
				envLine("SANDCASTLE_ROUTE_BROKER_CERT", RouteBrokerCertPath),
				envLine("SANDCASTLE_ROUTE_BROKER_KEY", RouteBrokerKeyPath),
				envLine("SANDCASTLE_REMOTE", admin.Remote),
				envLine("SANDCASTLE_STORAGE_POOL", admin.StoragePool),
				envLine("SANDCASTLE_CIDR_POOL", admin.CIDRPool),
				envLine("SANDCASTLE_PROJECT_PREFIX", admin.ProjectPrefix),
				envLine("SANDCASTLE_INFRA_PROJECT", admin.InfrastructureProject),
				envLine("SANDCASTLE_INFRA_HOST", admin.InfrastructureHost),
				envLine("SANDCASTLE_LETSENCRYPT_EMAIL", admin.LetsEncryptEmail),
				envLine("SANDCASTLE_BASE_IMAGE", admin.Images.Base),
				envLine("SANDCASTLE_AI_IMAGE", admin.Images.AI),
				"",
			}, "\n"),
			Mode: 0o600,
		},
		{
			Instance: RouteBrokerName,
			Path:     RouteBrokerCertPath,
			Content:  string(brokerTLS.CertificatePEM),
			Mode:     0o644,
		},
		{
			Instance: RouteBrokerName,
			Path:     RouteBrokerKeyPath,
			Content:  string(brokerTLS.PrivateKeyPEM),
			Mode:     0o600,
		},
		{
			Instance: RouteBrokerName,
			Path:     RouteBrokerUnitPath,
			Content: "[Unit]\nDescription=Sandcastle route broker\nAfter=network.target\n\n[Service]\nEnvironmentFile=" + RouteBrokerEnvPath + "\nExecStart=" + RouteBrokerBinaryPath + " admin route-broker serve --listen ${SANDCASTLE_ROUTE_BROKER_LISTEN} --cert ${SANDCASTLE_ROUTE_BROKER_CERT} --key ${SANDCASTLE_ROUTE_BROKER_KEY}\nRestart=on-failure\n\n[Install]\nWantedBy=multi-user.target\n",
			Mode:    0o644,
		},
	}
}

func envLine(key string, value string) string {
	return key + "=" + shellQuote(value)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func runtimeBinaries() []RuntimeBinary {
	source := strings.TrimSpace(os.Getenv("SANDCASTLE_BIN"))
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
	}
}

func runtimeCommands() []RuntimeCommand {
	return []RuntimeCommand{
		{
			Instance:    route.InfrastructureCaddyName,
			Description: "start infrastructure Caddy",
			Command: []string{"/bin/sh", "-lc", strings.Join([]string{
				"install -d /etc/caddy",
				"systemctl restart caddy",
				"for i in $(seq 1 50); do systemctl is-active caddy >/dev/null 2>&1 && exit 0; sleep 0.1; done",
				"systemctl is-active caddy",
			}, "; ")},
		},
		{
			Instance:    RouteBrokerName,
			Description: "start route broker service",
			Command: []string{"/bin/sh", "-lc", strings.Join([]string{
				"systemctl daemon-reload",
				"systemctl enable sandcastle-route-broker",
				"systemctl restart sandcastle-route-broker",
				"for i in $(seq 1 50); do systemctl is-active sandcastle-route-broker >/dev/null 2>&1 && exit 0; sleep 0.1; done",
				"systemctl is-active sandcastle-route-broker",
			}, "; ")},
		},
	}
}

func instancePlan(admin config.Admin, name string, role string) InstancePlan {
	devices := map[string]Device{
		"root": {
			"type": "disk",
			"pool": admin.StoragePool,
			"path": "/",
		},
	}
	if name == RouteBrokerName && strings.TrimSpace(admin.RouteBrokerIncusSocket) != "" {
		devices["incus-socket"] = Device{
			"type":   "disk",
			"source": strings.TrimSpace(admin.RouteBrokerIncusSocket),
			"path":   RouteBrokerIncusSocketPath,
		}
	}
	return InstancePlan{
		Name:       name,
		Role:       role,
		ImageAlias: admin.Images.Base,
		Config: map[string]string{
			meta.KeyKind:    "infrastructure",
			meta.KeyVersion: "1",
			meta.KeyName:    name,
			meta.KeyRole:    role,
		},
		Devices: devices,
		Start:   true,
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
		RuntimeInstances: []string{route.InfrastructureCaddyName, RouteBrokerName},
	}, nil
}
