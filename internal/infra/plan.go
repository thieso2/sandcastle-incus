package infra

import (
	"context"
	"fmt"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

const RouteBrokerName = "sc-route-broker"

type CreateRequest struct{}

type CreatePlan struct {
	Project               string            `json:"project"`
	StoragePool           string            `json:"storagePool"`
	CaddyInstance         string            `json:"caddyInstance"`
	RouteBrokerInstance   string            `json:"routeBrokerInstance"`
	ProjectMetadataConfig map[string]string `json:"projectMetadataConfig"`
	Instances             []InstancePlan    `json:"instances"`
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
	}, nil
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
