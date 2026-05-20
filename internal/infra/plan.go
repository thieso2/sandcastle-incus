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
	"github.com/thieso2/sandcastle-incus/internal/route"
)

const (
	RouteBrokerName       = "sc-route-broker"
	RouteBrokerListen     = ":9443"
	RouteBrokerBinaryPath = "/usr/local/bin/sandcastle"
	RouteBrokerCertPath   = "/etc/sandcastle/route-broker/tls.crt"
	RouteBrokerKeyPath    = "/etc/sandcastle/route-broker/tls.key"
	RouteBrokerEnvPath    = "/etc/sandcastle/route-broker/env"
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
		meta.KeyKind:    "infrastructure",
		meta.KeyVersion: "1",
		meta.KeyName:    project,
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
		RuntimeFiles:       runtimeFiles(brokerTLS),
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

func runtimeFiles(brokerTLS certs.KeyPair) []RuntimeFile {
	caddyFile := caddy.RenderInfrastructure(nil)
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
				"SANDCASTLE_ROUTE_BROKER_LISTEN=" + RouteBrokerListen,
				"SANDCASTLE_ROUTE_BROKER_CERT=" + RouteBrokerCertPath,
				"SANDCASTLE_ROUTE_BROKER_KEY=" + RouteBrokerKeyPath,
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
	}
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
				"if ! pgrep -x caddy >/dev/null 2>&1; then nohup caddy run --config /etc/caddy/Caddyfile >/var/log/caddy.log 2>&1 & fi",
				"for i in $(seq 1 50); do caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1 && exit 0; sleep 0.1; done",
				"pgrep -x caddy >/dev/null 2>&1",
			}, "; ")},
		},
		{
			Instance:    RouteBrokerName,
			Description: "start route broker service",
			Command: []string{"/bin/sh", "-lc", strings.Join([]string{
				"set -a",
				". " + RouteBrokerEnvPath,
				"set +a",
				"if ! pgrep -f '" + RouteBrokerBinaryPath + " admin route-broker serve' >/dev/null 2>&1; then nohup " + RouteBrokerBinaryPath + " admin route-broker serve --listen \"${SANDCASTLE_ROUTE_BROKER_LISTEN}\" --cert \"${SANDCASTLE_ROUTE_BROKER_CERT}\" --key \"${SANDCASTLE_ROUTE_BROKER_KEY}\" >/var/log/sandcastle-route-broker.log 2>&1 & fi",
				"sleep 0.2",
				"pgrep -f '" + RouteBrokerBinaryPath + " admin route-broker serve' >/dev/null 2>&1",
			}, "; ")},
		},
	}
}

func instancePlan(admin config.Admin, name string, role string) InstancePlan {
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
		Devices: map[string]Device{
			"root": {
				"type": "disk",
				"pool": admin.StoragePool,
				"path": "/",
			},
		},
		Start: true,
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
	return DeletePlan{
		Project:          project,
		RuntimeInstances: []string{route.InfrastructureCaddyName, RouteBrokerName},
	}, nil
}
