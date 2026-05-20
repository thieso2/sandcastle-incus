package sandbox

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/caddy"
	"github.com/thieso2/sandcastle-incus/internal/certs"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

const DefaultAppPort = 3000
const (
	TemplateAI   = "ai"
	TemplateBase = "base"
)
const (
	CaddyfilePath       = "/etc/caddy/Caddyfile"
	SandboxCertPath     = "/etc/caddy/certs/tls.crt"
	SandboxCertKeyPath  = "/etc/caddy/certs/tls.key"
	sandboxCertKeyMode  = 0o600
	sandboxCertFileMode = 0o644
)

type CreateRequest struct {
	Reference               string
	Template                string
	AppPort                 int
	HomeDir                 string
	WorkspaceDir            string
	ShareHome               bool
	ProjectCACertificatePEM []byte
	ProjectCAPrivateKeyPEM  []byte
}

type CreatePlan struct {
	Reference        string            `json:"reference"`
	Project          project.Summary   `json:"project"`
	Name             string            `json:"name"`
	InstanceName     string            `json:"instanceName"`
	PrivateIP        string            `json:"privateIP"`
	AppPort          int               `json:"appPort"`
	HomeDir          string            `json:"homeDir"`
	WorkspaceDir     string            `json:"workspaceDir"`
	StoragePool      string            `json:"storagePool"`
	CAVolume         string            `json:"caVolume"`
	Template         string            `json:"template"`
	ImageAlias       string            `json:"imageAlias"`
	MetadataConfig   map[string]string `json:"metadataConfig"`
	Devices          map[string]Device `json:"devices"`
	StartsByDefault  bool              `json:"startsByDefault"`
	CaddyFile        caddy.File        `json:"caddyFile"`
	CertificateFiles []File            `json:"certificateFiles,omitempty"`
}

type Device map[string]string

type File struct {
	Path    string `json:"path"`
	Content []byte `json:"-"`
	Mode    int    `json:"mode"`
}

type Creator interface {
	CreateSandbox(context.Context, CreatePlan) error
}

type Store interface {
	ListSandboxes(ctx context.Context, summary project.Summary) ([]meta.Sandbox, error)
}

func PlanCreate(ctx context.Context, admin config.Admin, store project.IncusProjectStore, sandboxStore Store, request CreateRequest) (CreatePlan, error) {
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
	template := request.Template
	if template == "" {
		template = summary.DefaultTemplate
	}
	if template == "" {
		template = TemplateAI
	}
	imageAlias, err := imageAliasForTemplate(admin, template)
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
	existingSandboxes, err := listExistingSandboxes(ctx, sandboxStore, summary)
	if err != nil {
		return CreatePlan{}, err
	}
	privateIP, err := allocateSandboxIP(summary.PrivateCIDR, sandboxName, existingSandboxes)
	if err != nil {
		return CreatePlan{}, err
	}
	homeDir := request.HomeDir
	if homeDir == "" {
		homeDir = "."
	}
	workspaceDir := request.WorkspaceDir
	if workspaceDir == "" {
		workspaceDir = "."
	}
	if err := validateHomeSharing(sandboxName, homeDir, request.ShareHome, existingSandboxes); err != nil {
		return CreatePlan{}, err
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
	hostname := sandboxName + "." + summary.Domain
	caddyFile := caddy.RenderSandbox(hostname, appPort, SandboxCertPath, SandboxCertKeyPath)
	certificateFiles, err := certificateFilesFromRequest(request, sandboxName, summary.Domain)
	if err != nil {
		return CreatePlan{}, err
	}
	return CreatePlan{
		Reference:      request.Reference,
		Project:        summary,
		Name:           sandboxName,
		InstanceName:   instanceName,
		PrivateIP:      privateIP,
		AppPort:        appPort,
		HomeDir:        homeDir,
		WorkspaceDir:   workspaceDir,
		StoragePool:    admin.StoragePool,
		CAVolume:       project.CAVolumeName,
		Template:       template,
		ImageAlias:     imageAlias,
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
		StartsByDefault:  true,
		CaddyFile:        caddyFile,
		CertificateFiles: certificateFiles,
	}, nil
}

func imageAliasForTemplate(admin config.Admin, template string) (string, error) {
	switch template {
	case TemplateAI:
		return admin.Images.AI, nil
	case TemplateBase:
		return admin.Images.Base, nil
	default:
		return "", fmt.Errorf("unsupported sandbox template %q", template)
	}
}

func validateHomeSharing(sandboxName string, homeDir string, shareHome bool, existing []meta.Sandbox) error {
	if shareHome {
		return nil
	}
	for _, sandbox := range existing {
		if sandbox.Name == sandboxName || !sandbox.Running {
			continue
		}
		if sandbox.HomeDir == homeDir {
			return fmt.Errorf("home directory %q is already used by running sandbox %s; pass --share-home to confirm sharing", homeDir, sandbox.Name)
		}
	}
	return nil
}

func certificateFilesFromRequest(request CreateRequest, sandboxName string, domain string) ([]File, error) {
	if len(request.ProjectCACertificatePEM) == 0 && len(request.ProjectCAPrivateKeyPEM) == 0 {
		return nil, nil
	}
	if len(request.ProjectCACertificatePEM) == 0 || len(request.ProjectCAPrivateKeyPEM) == 0 {
		return nil, fmt.Errorf("project CA certificate and private key are both required to issue a sandbox certificate")
	}
	return IssueCertificateFiles(sandboxName, domain, request.ProjectCACertificatePEM, request.ProjectCAPrivateKeyPEM)
}

func IssueCertificateFiles(sandboxName string, domain string, caCertPEM []byte, caKeyPEM []byte) ([]File, error) {
	return IssueCertificateFilesWithExtraSANs(sandboxName, domain, nil, caCertPEM, caKeyPEM)
}

func IssueCertificateFilesWithExtraSANs(sandboxName string, domain string, extraSANs []string, caCertPEM []byte, caKeyPEM []byte) ([]File, error) {
	hostname := sandboxName + "." + domain
	leaf, err := certs.IssueSandboxLeaf(
		caCertPEM,
		caKeyPEM,
		hostname,
		certs.SandboxDNSNames(sandboxName, domain, extraSANs),
		time.Now().UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("issue sandbox certificate: %w", err)
	}
	return []File{
		{Path: SandboxCertPath, Content: leaf.CertificatePEM, Mode: sandboxCertFileMode},
		{Path: SandboxCertKeyPath, Content: leaf.PrivateKeyPEM, Mode: sandboxCertKeyMode},
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

func listExistingSandboxes(ctx context.Context, store Store, summary project.Summary) ([]meta.Sandbox, error) {
	if store == nil {
		return nil, nil
	}
	sandboxes, err := store.ListSandboxes(ctx, summary)
	if err != nil {
		return nil, fmt.Errorf("list existing sandboxes for %s/%s: %w", summary.Owner, summary.Name, err)
	}
	return sandboxes, nil
}

func allocateSandboxIP(cidr string, sandboxName string, existing []meta.Sandbox) (string, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return "", err
	}
	for _, sandbox := range existing {
		if sandbox.Name == sandboxName && sandbox.PrivateIP != "" {
			addr, err := netip.ParseAddr(sandbox.PrivateIP)
			if err != nil {
				return "", fmt.Errorf("existing sandbox %s has invalid private IP %q", sandboxName, sandbox.PrivateIP)
			}
			if !prefix.Contains(addr) {
				return "", fmt.Errorf("existing sandbox %s private IP %s is outside %s", sandboxName, sandbox.PrivateIP, cidr)
			}
			return sandbox.PrivateIP, nil
		}
	}
	used := map[netip.Addr]bool{}
	for _, sandbox := range existing {
		addr, err := netip.ParseAddr(sandbox.PrivateIP)
		if err == nil && prefix.Contains(addr) {
			used[addr] = true
		}
	}
	base := prefix.Masked().Addr().As4()
	for host := byte(20); host <= 199; host++ {
		base[3] = host
		candidate := netip.AddrFrom4(base)
		if !prefix.Contains(candidate) {
			continue
		}
		if !used[candidate] {
			return candidate.String(), nil
		}
	}
	return "", fmt.Errorf("no free sandbox private IPs in %s", cidr)
}

func (p CreatePlan) AppPortString() string {
	return strconv.Itoa(p.AppPort)
}
