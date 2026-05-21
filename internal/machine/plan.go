package machine

import (
	"context"
	"fmt"
	"net/netip"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/caddy"
	"github.com/thieso2/sandcastle-incus/internal/certs"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

const DefaultAppPort = 3000
const (
	DefaultLinuxUID = 1000
	DefaultLinuxGID = 1000
)
const (
	TemplateAI   = "ai"
	TemplateBase = "base"
)
const (
	CaddyfilePath       = "/etc/caddy/Caddyfile"
	MachineCertPath     = "/etc/caddy/certs/tls.crt"
	MachineCertKeyPath  = "/etc/caddy/certs/tls.key"
	machineCertKeyMode  = 0o600
	machineCertFileMode = 0o644
)

type CreateRequest struct {
	Reference              string
	Template               string
	AppPort                int
	HomeDir                string
	WorkspaceDir           string
	ShareHome              bool
	ContainerTools         bool
	TenantCACertificatePEM []byte
	TenantCAPrivateKeyPEM  []byte
}

type CreatePlan struct {
	Reference        string            `json:"reference"`
	Tenant           project.Summary   `json:"tenant"`
	Project          string            `json:"project"`
	Name             string            `json:"name"`
	InstanceName     string            `json:"instanceName"`
	Hostname         string            `json:"hostname"`
	PrivateIP        string            `json:"privateIP"`
	AppPort          int               `json:"appPort"`
	LinuxUser        string            `json:"linuxUser"`
	SSHPublicKey     string            `json:"sshPublicKey,omitempty"`
	HomeDir          string            `json:"homeDir"`
	WorkspaceDir     string            `json:"workspaceDir"`
	StoragePool      string            `json:"storagePool"`
	CAVolume         string            `json:"caVolume"`
	Template         string            `json:"template"`
	ImageAlias       string            `json:"imageAlias"`
	ContainerTools   bool              `json:"containerTools"`
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
	CreateMachine(context.Context, CreatePlan) error
}

type Store interface {
	ListMachines(ctx context.Context, summary project.Summary) ([]meta.Machine, error)
}

type UnmanagedMachine struct {
	Tenant       string `json:"tenant"`
	Name         string `json:"name"`
	InstanceName string `json:"instanceName"`
	Type         string `json:"type,omitempty"`
	Status       string `json:"status,omitempty"`
	CreatedAt    string `json:"createdAt,omitempty"`
	Running      bool   `json:"running"`
}

type UnmanagedStore interface {
	ListUnmanagedMachines(ctx context.Context, summary project.Summary) ([]UnmanagedMachine, error)
}

func PlanCreate(ctx context.Context, admin config.Admin, store project.IncusProjectStore, machineStore Store, request CreateRequest) (CreatePlan, error) {
	if err := admin.Validate(); err != nil {
		return CreatePlan{}, err
	}
	tenantRef, err := currentTenantRef(admin)
	if err != nil {
		return CreatePlan{}, err
	}
	projectRef, machineName, err := naming.ParseUserMachineRef(request.Reference, admin.Project)
	if err != nil {
		return CreatePlan{}, err
	}
	summary, err := findTenant(ctx, store, tenantRef)
	if err != nil {
		return CreatePlan{}, err
	}
	if !tenantHasProject(summary, projectRef.Project) {
		return CreatePlan{}, fmt.Errorf("Sandcastle project %s not found in tenant %s", projectRef.Project, summary.Tenant)
	}
	template := request.Template
	if template == "" {
		template = TemplateAI
	}
	imageAlias, err := imageAliasForTemplate(admin, template)
	if err != nil {
		return CreatePlan{}, err
	}
	appPort := request.AppPort
	if appPort == 0 {
		appPort, err = DefaultAppPortForTemplate(template)
		if err != nil {
			return CreatePlan{}, err
		}
	}
	if appPort < 1 || appPort > 65535 {
		return CreatePlan{}, fmt.Errorf("invalid app port %d", appPort)
	}
	linuxUser := summary.Tenant
	existingMachines, err := listExistingMachines(ctx, machineStore, summary)
	if err != nil {
		return CreatePlan{}, err
	}
	privateIP, err := allocateMachineIP(summary.PrivateCIDR, projectRef.Project, machineName, existingMachines)
	if err != nil {
		return CreatePlan{}, err
	}
	homeDir, err := normalizeStorageSubdir("home", request.HomeDir, projectRef.Project, machineName)
	if err != nil {
		return CreatePlan{}, err
	}
	workspaceDir, err := normalizeStorageSubdir("workspace", request.WorkspaceDir, projectRef.Project, machineName)
	if err != nil {
		return CreatePlan{}, err
	}
	if err := validateHomeSharing(projectRef.Project, machineName, homeDir, request.ShareHome, existingMachines); err != nil {
		return CreatePlan{}, err
	}
	state := meta.Machine{
		Tenant:         summary.Tenant,
		Project:        projectRef.Project,
		Name:           machineName,
		Type:           meta.MachineTypeContainer,
		Template:       template,
		AppPort:        appPort,
		PrivateIP:      privateIP,
		LinuxUser:      linuxUser,
		HomeDir:        homeDir,
		WorkspaceDir:   workspaceDir,
		ContainerTools: request.ContainerTools,
	}
	metadataConfig, err := meta.MachineConfig(state)
	if err != nil {
		return CreatePlan{}, err
	}
	machineRef := naming.MachineRef{Tenant: summary.Tenant, Project: projectRef.Project, Machine: machineName}
	instanceName, err := naming.MachineIncusInstanceName(machineRef)
	if err != nil {
		return CreatePlan{}, err
	}
	hostname := machineName + "." + projectRef.Project + "." + summary.DNSSuffix
	caddyFile := caddy.RenderSandbox(hostname, appPort, MachineCertPath, MachineCertKeyPath)
	certificateFiles, err := certificateFilesFromRequest(request, machineName, projectRef.Project, summary.DNSSuffix)
	if err != nil {
		return CreatePlan{}, err
	}
	return CreatePlan{
		Reference:      request.Reference,
		Tenant:         summary,
		Project:        projectRef.Project,
		Name:           machineName,
		InstanceName:   instanceName,
		Hostname:       hostname,
		PrivateIP:      privateIP,
		AppPort:        appPort,
		LinuxUser:      linuxUser,
		HomeDir:        homeDir,
		WorkspaceDir:   workspaceDir,
		StoragePool:    summary.IncusName,
		CAVolume:       project.CAVolumeName,
		Template:       template,
		ImageAlias:     imageAlias,
		ContainerTools: request.ContainerTools,
		MetadataConfig: metadataConfig,
		Devices: map[string]Device{
			"root": {
				"type": "disk",
				"pool": summary.IncusName,
				"path": "/",
			},
			"eth0": {
				"type":         "nic",
				"nictype":      "bridged",
				"parent":       project.PrivateNetworkName(summary.IncusName),
				"ipv4.address": privateIP,
			},
			"home": {
				"type":   "disk",
				"pool":   summary.IncusName,
				"source": project.HomeVolumeName + "/" + homeDir,
				"path":   "/home/" + linuxUser,
			},
			"workspace": {
				"type":   "disk",
				"pool":   summary.IncusName,
				"source": project.WorkspaceVolumeName + "/" + workspaceDir,
				"path":   "/workspace",
			},
		},
		StartsByDefault:  true,
		CaddyFile:        caddyFile,
		CertificateFiles: certificateFiles,
		SSHPublicKey:     summary.SSHPublicKey,
	}, nil
}

func imageAliasForTemplate(admin config.Admin, template string) (string, error) {
	switch template {
	case TemplateAI:
		return admin.Images.AI, nil
	case TemplateBase:
		return admin.Images.Base, nil
	default:
		return "", fmt.Errorf("unsupported machine template %q", template)
	}
}

func DefaultAppPortForTemplate(template string) (int, error) {
	switch strings.TrimSpace(template) {
	case "", TemplateAI, TemplateBase:
		return DefaultAppPort, nil
	default:
		return 0, fmt.Errorf("unsupported machine template %q", template)
	}
}

func validateHomeSharing(projectName string, machineName string, homeDir string, shareHome bool, existing []meta.Machine) error {
	if shareHome {
		return nil
	}
	for _, machine := range existing {
		if machine.Project == projectName && machine.Name == machineName || !machine.Running {
			continue
		}
		if machine.HomeDir == homeDir {
			return fmt.Errorf("home directory %q is already used by running machine %s/%s; pass --share-home to confirm sharing", homeDir, machine.Project, machine.Name)
		}
	}
	return nil
}

func normalizeStorageSubdir(kind string, value string, projectName string, machineName string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return path.Join(projectName, machineName), nil
	}
	if strings.Contains(value, "\\") {
		return "", fmt.Errorf("%s directory %q must use forward-slash relative paths", kind, value)
	}
	if path.IsAbs(value) {
		return "", fmt.Errorf("%s directory %q must be relative to the tenant storage volume", kind, value)
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return "", fmt.Errorf("%s directory %q must not contain .. path segments", kind, value)
		}
	}
	cleaned := path.Clean(value)
	if cleaned == "/" || cleaned == "." {
		return ".", nil
	}
	return cleaned, nil
}

func certificateFilesFromRequest(request CreateRequest, machineName string, projectName string, suffix string) ([]File, error) {
	if len(request.TenantCACertificatePEM) == 0 && len(request.TenantCAPrivateKeyPEM) == 0 {
		return nil, nil
	}
	if len(request.TenantCACertificatePEM) == 0 || len(request.TenantCAPrivateKeyPEM) == 0 {
		return nil, fmt.Errorf("tenant CA certificate and private key are both required to issue a machine certificate")
	}
	return IssueCertificateFiles(machineName, projectName, suffix, request.TenantCACertificatePEM, request.TenantCAPrivateKeyPEM)
}

func IssueCertificateFiles(machineName string, projectName string, suffix string, caCertPEM []byte, caKeyPEM []byte) ([]File, error) {
	return IssueCertificateFilesWithExtraSANs(machineName, projectName, suffix, nil, caCertPEM, caKeyPEM)
}

func IssueCertificateFilesWithExtraSANs(machineName string, projectName string, suffix string, extraSANs []string, caCertPEM []byte, caKeyPEM []byte) ([]File, error) {
	hostname := machineName + "." + projectName + "." + suffix
	leaf, err := certs.IssueSandboxLeaf(
		caCertPEM,
		caKeyPEM,
		hostname,
		certs.SandboxDNSNames(machineName+"."+projectName, suffix, extraSANs),
		time.Now().UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("issue machine certificate: %w", err)
	}
	return []File{
		{Path: MachineCertPath, Content: leaf.CertificatePEM, Mode: machineCertFileMode},
		{Path: MachineCertKeyPath, Content: leaf.PrivateKeyPEM, Mode: machineCertKeyMode},
	}, nil
}

func currentTenantRef(admin config.Admin) (naming.TenantRef, error) {
	ref, err := naming.ParseTenantRef(admin.Tenant)
	if err != nil {
		return naming.TenantRef{}, fmt.Errorf("tenant is required; set SANDCASTLE_TENANT or local tenant config")
	}
	return ref, nil
}

func findTenant(ctx context.Context, store project.IncusProjectStore, ref naming.TenantRef) (project.Summary, error) {
	tenants, err := project.List(ctx, store)
	if err != nil {
		return project.Summary{}, err
	}
	for _, summary := range tenants {
		if summary.Tenant == ref.Tenant {
			return summary, nil
		}
	}
	return project.Summary{}, fmt.Errorf("Sandcastle tenant %s not found", ref.String())
}

func tenantHasProject(summary project.Summary, projectName string) bool {
	for _, candidate := range summary.Projects {
		if candidate.Name == projectName {
			return true
		}
	}
	return false
}

func listExistingMachines(ctx context.Context, store Store, summary project.Summary) ([]meta.Machine, error) {
	if store == nil {
		return nil, nil
	}
	machines, err := store.ListMachines(ctx, summary)
	if err != nil {
		return nil, fmt.Errorf("list existing machines for %s: %w", summary.Tenant, err)
	}
	return machines, nil
}

func allocateMachineIP(cidr string, projectName string, machineName string, existing []meta.Machine) (string, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return "", err
	}
	for _, machine := range existing {
		if machine.Project == projectName && machine.Name == machineName && machine.PrivateIP != "" {
			addr, err := netip.ParseAddr(machine.PrivateIP)
			if err != nil {
				return "", fmt.Errorf("existing machine %s/%s has invalid private IP %q", projectName, machineName, machine.PrivateIP)
			}
			if !prefix.Contains(addr) {
				return "", fmt.Errorf("existing machine %s/%s private IP %s is outside %s", projectName, machineName, machine.PrivateIP, cidr)
			}
			return machine.PrivateIP, nil
		}
	}
	used := map[netip.Addr]bool{}
	for _, machine := range existing {
		addr, err := netip.ParseAddr(machine.PrivateIP)
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
	return "", fmt.Errorf("no free machine private IPs in %s", cidr)
}

func (p CreatePlan) AppPortString() string {
	return strconv.Itoa(p.AppPort)
}
