package infra

import (
	"context"
	"fmt"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/caddy"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

const (
	RouteBrokerName        = "sc-route-broker"
	RouteBrokerListen      = ":9443"
	RouteBrokerCertPath    = "/etc/sandcastle/route-broker/tls.crt"
	RouteBrokerKeyPath     = "/etc/sandcastle/route-broker/tls.key"
	RouteBrokerServicePath = "/etc/systemd/system/sandcastle-route-broker.service"
	RouteBrokerEnvPath     = "/etc/sandcastle/route-broker/env"
)

type CreateRequest struct{}

type CreatePlan struct {
	Project               string             `json:"project"`
	StoragePool           string             `json:"storagePool"`
	CaddyInstance         string             `json:"caddyInstance"`
	RouteBrokerInstance   string             `json:"routeBrokerInstance"`
	ProjectMetadataConfig map[string]string  `json:"projectMetadataConfig"`
	Instances             []InstancePlan     `json:"instances"`
	RuntimeDirectories    []RuntimeDirectory `json:"runtimeDirectories"`
	RuntimeFiles          []RuntimeFile      `json:"runtimeFiles"`
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

type Creator interface {
	CreateInfrastructure(context.Context, CreatePlan) error
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
		RuntimeFiles:       runtimeFiles(),
	}, nil
}

func runtimeDirectories() []RuntimeDirectory {
	return []RuntimeDirectory{
		{Instance: route.InfrastructureCaddyName, Path: "/etc/caddy", Mode: 0o755},
		{Instance: RouteBrokerName, Path: "/etc/sandcastle", Mode: 0o755},
		{Instance: RouteBrokerName, Path: "/etc/sandcastle/route-broker", Mode: 0o700},
		{Instance: RouteBrokerName, Path: "/etc/systemd/system", Mode: 0o755},
	}
}

func runtimeFiles() []RuntimeFile {
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
			Path:     RouteBrokerServicePath,
			Content: `[Unit]
Description=Sandcastle route broker
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=/etc/sandcastle/route-broker/env
ExecStart=/usr/local/bin/sandcastle admin route-broker serve --listen ${SANDCASTLE_ROUTE_BROKER_LISTEN} --cert ${SANDCASTLE_ROUTE_BROKER_CERT} --key ${SANDCASTLE_ROUTE_BROKER_KEY}
Restart=on-failure

[Install]
WantedBy=multi-user.target
`,
			Mode: 0o644,
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
