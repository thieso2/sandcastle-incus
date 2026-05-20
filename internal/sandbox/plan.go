package sandbox

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

const DefaultAppPort = 3000

type CreateRequest struct {
	Reference    string
	AppPort      int
	HomeDir      string
	WorkspaceDir string
}

type CreatePlan struct {
	Reference       string            `json:"reference"`
	Project         project.Summary   `json:"project"`
	Name            string            `json:"name"`
	InstanceName    string            `json:"instanceName"`
	PrivateIP       string            `json:"privateIP"`
	AppPort         int               `json:"appPort"`
	HomeDir         string            `json:"homeDir"`
	WorkspaceDir    string            `json:"workspaceDir"`
	ImageAlias      string            `json:"imageAlias"`
	MetadataConfig  map[string]string `json:"metadataConfig"`
	Devices         map[string]Device `json:"devices"`
	StartsByDefault bool              `json:"startsByDefault"`
}

type Device map[string]string

func PlanCreate(ctx context.Context, admin config.Admin, store project.IncusProjectStore, request CreateRequest) (CreatePlan, error) {
	if err := admin.Validate(); err != nil {
		return CreatePlan{}, err
	}
	projectRef, sandboxName, err := parseSandboxRef(request.Reference)
	if err != nil {
		return CreatePlan{}, err
	}
	if naming.IsReservedSandboxName(sandboxName) {
		return CreatePlan{}, fmt.Errorf("sandbox name %q is reserved", sandboxName)
	}
	summary, err := findProject(ctx, store, projectRef)
	if err != nil {
		return CreatePlan{}, err
	}
	appPort := request.AppPort
	if appPort == 0 {
		appPort = DefaultAppPort
	}
	if appPort < 1 || appPort > 65535 {
		return CreatePlan{}, fmt.Errorf("invalid app port %d", appPort)
	}
	privateIP, err := firstSandboxIP(summary.PrivateCIDR)
	if err != nil {
		return CreatePlan{}, err
	}
	homeDir := request.HomeDir
	if homeDir == "" {
		homeDir = sandboxName
	}
	workspaceDir := request.WorkspaceDir
	if workspaceDir == "" {
		workspaceDir = sandboxName
	}
	state := meta.Sandbox{
		Owner:        projectRef.Owner,
		Project:      projectRef.Project,
		Name:         sandboxName,
		AppPort:      appPort,
		PrivateIP:    privateIP,
		HomeDir:      homeDir,
		WorkspaceDir: workspaceDir,
	}
	metadataConfig, err := meta.SandboxConfig(state)
	if err != nil {
		return CreatePlan{}, err
	}
	instanceName := "sc-" + sandboxName
	return CreatePlan{
		Reference:      request.Reference,
		Project:        summary,
		Name:           sandboxName,
		InstanceName:   instanceName,
		PrivateIP:      privateIP,
		AppPort:        appPort,
		HomeDir:        homeDir,
		WorkspaceDir:   workspaceDir,
		ImageAlias:     admin.Images.AI,
		MetadataConfig: metadataConfig,
		Devices: map[string]Device{
			"root": {
				"type": "disk",
				"pool": admin.StoragePool,
				"path": "/",
			},
			"eth0": {
				"type":         "nic",
				"nictype":      "bridged",
				"parent":       project.PrivateNetworkName,
				"ipv4.address": privateIP,
			},
			"home": {
				"type":   "disk",
				"source": project.HomeVolumeName + "/" + homeDir,
				"path":   "/home/sandcastle",
			},
			"workspace": {
				"type":   "disk",
				"source": project.WorkspaceVolumeName + "/" + workspaceDir,
				"path":   "/workspace",
			},
		},
		StartsByDefault: true,
	}, nil
}

func parseSandboxRef(value string) (naming.ProjectRef, string, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 3 {
		return naming.ProjectRef{}, "", fmt.Errorf("sandbox reference must be owner/project/name")
	}
	projectRef, err := naming.ParseProjectRef(parts[0] + "/" + parts[1])
	if err != nil {
		return naming.ProjectRef{}, "", err
	}
	if err := (naming.ProjectRef{Owner: parts[2], Project: "placeholder"}).Validate(); err != nil {
		return naming.ProjectRef{}, "", fmt.Errorf("invalid sandbox name %q", parts[2])
	}
	return projectRef, parts[2], nil
}

func findProject(ctx context.Context, store project.IncusProjectStore, ref naming.ProjectRef) (project.Summary, error) {
	projects, err := project.List(ctx, store)
	if err != nil {
		return project.Summary{}, err
	}
	for _, summary := range projects {
		if summary.Owner == ref.Owner && summary.Name == ref.Project {
			return summary, nil
		}
	}
	return project.Summary{}, fmt.Errorf("Sandcastle project %s not found", ref.String())
}

func firstSandboxIP(cidr string) (string, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return "", err
	}
	addr := prefix.Masked().Addr().As4()
	addr[3] = 20
	candidate := netip.AddrFrom4(addr)
	if !prefix.Contains(candidate) {
		return "", fmt.Errorf("sandbox address .20 is outside %s", cidr)
	}
	return candidate.String(), nil
}

func (p CreatePlan) AppPortString() string {
	return strconv.Itoa(p.AppPort)
}
