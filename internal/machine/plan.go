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
	"github.com/thieso2/sandcastle-incus/internal/share"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
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
	WorkloadIdentity       *WorkloadIdentityRequest
}

type CreatePlan struct {
	Reference        string            `json:"reference"`
	Tenant           tenant.Summary    `json:"tenant"`
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
	DockerAutostart  bool              `json:"dockerAutostart,omitempty"`
	MetadataConfig   map[string]string `json:"metadataConfig"`
	Devices          map[string]Device `json:"devices"`
	StartsByDefault  bool              `json:"startsByDefault"`
	CaddyFile        caddy.File        `json:"caddyFile"`
	CertificateFiles []File            `json:"certificateFiles,omitempty"`
	WorkloadFiles    []File            `json:"workloadFiles,omitempty"`
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
	ListMachines(ctx context.Context, summary tenant.Summary) ([]meta.Machine, error)
}

type UnmanagedMachine struct {
	Tenant       string `json:"tenant"`
	Name         string `json:"name"`
	InstanceName string `json:"instanceName"`
	Type         string `json:"type,omitempty"`
	PrivateIP    string `json:"privateIp,omitempty"`
	Status       string `json:"status,omitempty"`
	CreatedAt    string `json:"createdAt,omitempty"`
	Running      bool   `json:"running"`
}

type UnmanagedStore interface {
	ListUnmanagedMachines(ctx context.Context, summary tenant.Summary) ([]UnmanagedMachine, error)
}

type CombinedStore interface {
	ListMachinesAndUnmanaged(ctx context.Context, summary tenant.Summary) ([]meta.Machine, []UnmanagedMachine, error)
}

func PlanCreate(ctx context.Context, admin config.Admin, store tenant.IncusTenantStore, machineStore Store, request CreateRequest) (CreatePlan, error) {
	if err := admin.Validate(); err != nil {
		return CreatePlan{}, err
	}
	target, err := parseMachineTarget(ctx, admin, store, request.Reference)
	if err != nil {
		return CreatePlan{}, err
	}
	summary := target.Summary
	if !tenantHasProject(summary, target.Project) {
		return CreatePlan{}, fmt.Errorf("Sandcastle project %s not found in tenant %s", target.Project, summary.Tenant)
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
	linuxUser := strings.TrimSpace(summary.UnixUser)
	if linuxUser == "" {
		linuxUser = summary.Tenant
	}
	if err := naming.ValidateUnixUsername(linuxUser); err != nil {
		return CreatePlan{}, err
	}
	projectConfig, ok := findProjectConfig(summary, target.Project)
	if !ok {
		return CreatePlan{}, fmt.Errorf("Sandcastle project %s not found in tenant %s", target.Project, summary.Tenant)
	}
	containerTools := request.ContainerTools || projectConfig.DockerAutostart
	existingMachines, err := listExistingMachines(ctx, machineStore, summary)
	if err != nil {
		return CreatePlan{}, err
	}
	privateIP, err := allocateMachineIP(summary.PrivateCIDR, target.Project, target.Name, existingMachines)
	if err != nil {
		return CreatePlan{}, err
	}
	homeDir, err := normalizeStorageSubdir("home", request.HomeDir, target.Project, target.Name)
	if err != nil {
		return CreatePlan{}, err
	}
	workspaceDir, err := normalizeStorageSubdir("workspace", request.WorkspaceDir, target.Project, target.Name)
	if err != nil {
		return CreatePlan{}, err
	}
	state := meta.Machine{
		Tenant:          summary.Tenant,
		Project:         target.Project,
		Name:            target.Name,
		Type:            meta.MachineTypeContainer,
		Template:        template,
		AppPort:         appPort,
		PrivateIP:       privateIP,
		LinuxUser:       linuxUser,
		HomeDir:         homeDir,
		WorkspaceDir:    workspaceDir,
		ContainerTools:  containerTools,
		DockerAutostart: projectConfig.DockerAutostart,
	}
	metadataConfig, err := meta.MachineConfig(state)
	if err != nil {
		return CreatePlan{}, err
	}
	machineRef := naming.MachineRef{Tenant: summary.Tenant, Project: target.Project, Machine: target.Name}
	instanceName, err := naming.MachineIncusInstanceName(machineRef)
	if err != nil {
		return CreatePlan{}, err
	}
	hostname := MachineHostname(target.Name, target.Project, summary.DNSSuffix)
	caddyFile := caddy.RenderMachineHosts(MachineCaddyHostnames(target.Name, target.Project, summary.DNSSuffix), appPort, MachineCertPath, MachineCertKeyPath)
	certificateFiles, err := certificateFilesFromRequest(request, target.Name, target.Project, summary.DNSSuffix)
	if err != nil {
		return CreatePlan{}, err
	}
	workloadFiles, err := WorkloadIdentityFiles(request.WorkloadIdentity)
	if err != nil {
		return CreatePlan{}, err
	}
	devices, err := createDevices(admin, summary, privateIP, linuxUser, homeDir, workspaceDir)
	if err != nil {
		return CreatePlan{}, err
	}
	return CreatePlan{
		Reference:        request.Reference,
		Tenant:           summary,
		Project:          target.Project,
		Name:             target.Name,
		InstanceName:     instanceName,
		Hostname:         hostname,
		PrivateIP:        privateIP,
		AppPort:          appPort,
		LinuxUser:        linuxUser,
		HomeDir:          homeDir,
		WorkspaceDir:     workspaceDir,
		StoragePool:      summary.IncusName,
		CAVolume:         tenant.CAVolumeName,
		Template:         template,
		ImageAlias:       imageAlias,
		ContainerTools:   containerTools,
		DockerAutostart:  projectConfig.DockerAutostart,
		MetadataConfig:   metadataConfig,
		Devices:          devices,
		StartsByDefault:  true,
		CaddyFile:        caddyFile,
		CertificateFiles: certificateFiles,
		WorkloadFiles:    workloadFiles,
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

func findProjectConfig(summary tenant.Summary, projectName string) (meta.Project, bool) {
	for _, project := range summary.Projects {
		if project.Name == projectName {
			return project, true
		}
	}
	return meta.Project{}, false
}

func DefaultAppPortForTemplate(template string) (int, error) {
	switch strings.TrimSpace(template) {
	case "", TemplateAI, TemplateBase:
		return DefaultAppPort, nil
	default:
		return 0, fmt.Errorf("unsupported machine template %q", template)
	}
}

func createDevices(admin config.Admin, summary tenant.Summary, privateIP string, linuxUser string, homeDir string, workspaceDir string) (map[string]Device, error) {
	devices := map[string]Device{
		"root": {
			"type": "disk",
			"pool": summary.IncusName,
			"path": "/",
		},
		"eth0": {
			"type":         "nic",
			"nictype":      "bridged",
			"parent":       tenant.PrivateNetworkName(summary.IncusName),
			"ipv4.address": privateIP,
		},
		"home": {
			"type":   "disk",
			"pool":   summary.IncusName,
			"source": tenant.HomeVolumeName + "/" + homeDir,
			"path":   "/home/" + linuxUser,
		},
		"workspace": {
			"type":   "disk",
			"pool":   summary.IncusName,
			"source": tenant.WorkspaceVolumeName + "/" + workspaceDir,
			"path":   "/workspace",
		},
	}
	for _, storageShare := range summary.StorageShares {
		if !share.IsAcceptedAvailable(storageShare, summary.Tenant) {
			continue
		}
		sourceIncusProject, err := naming.TenantIncusProjectNameWithPrefix(admin.IncusProjectPrefix, naming.TenantRef{Tenant: storageShare.SourceTenant})
		if err != nil {
			return nil, err
		}
		deviceName := share.DeviceName(storageShare)
		devices[deviceName] = Device(share.DesiredDevice(storageShare, sourceIncusProject, tenant.WorkspaceVolumeName))
	}
	return devices, nil
}

func normalizeStorageSubdir(kind string, value string, projectName string, _ string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return projectName, nil
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
	hostname := MachineHostname(machineName, projectName, suffix)
	leaf, err := certs.IssueMachineLeaf(
		caCertPEM,
		caKeyPEM,
		hostname,
		certs.MachineDNSNames(machineName+"."+projectName, suffix, extraSANs),
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

func MachineHostname(machineName string, projectName string, suffix string) string {
	return machineName + "." + projectName + "." + suffix
}

func ShortMachineHostname(machineName string, projectName string) string {
	return machineName + "." + projectName
}

func MachineCaddyHostnames(machineName string, projectName string, suffix string) []string {
	fullHostname := MachineHostname(machineName, projectName, suffix)
	shortHostname := ShortMachineHostname(machineName, projectName)
	return []string{
		fullHostname,
		"*." + fullHostname,
		shortHostname,
		"*." + shortHostname,
	}
}

func currentTenantRef(admin config.Admin) (naming.TenantRef, error) {
	ref, err := naming.ParseTenantRef(admin.Tenant)
	if err != nil {
		return naming.TenantRef{}, fmt.Errorf("tenant is required; set SANDCASTLE_TENANT or local tenant config")
	}
	return ref, nil
}

type machineTarget struct {
	Summary         tenant.Summary
	Project         string
	Name            string
	ExplicitTenant  bool
	ExplicitProject bool
	ProjectSyntax   string
}

func parseMachineTarget(ctx context.Context, admin config.Admin, store tenant.IncusTenantStore, reference string) (machineTarget, error) {
	currentTenant := strings.TrimSpace(admin.Tenant)
	projectSyntax := strings.TrimSpace(reference)
	if tenantName, rest, ok := explicitTenantProjectMachineReference(projectSyntax); ok {
		return parseMachineTargetInTenant(ctx, admin, store, tenantName, rest, true)
	}
	if tenantName, rest, ok := possibleTenantMachineReference(projectSyntax); ok {
		if target, ok, err := tryParseTenantMachineTarget(ctx, admin, store, tenantName, rest); err != nil || ok {
			return target, err
		}
	}
	return parseMachineTargetInTenant(ctx, admin, store, currentTenant, projectSyntax, false)
}

func parseMachineTargetInTenant(ctx context.Context, admin config.Admin, store tenant.IncusTenantStore, tenantName string, projectSyntax string, explicitTenant bool) (machineTarget, error) {
	currentTenant := strings.TrimSpace(tenantName)
	if currentTenant == "" {
		return machineTarget{}, fmt.Errorf("tenant is required; set SANDCASTLE_TENANT or local tenant config")
	}
	if err := naming.ValidateTenantName(currentTenant); err != nil {
		return machineTarget{}, err
	}
	projectRef, machineName, err := naming.ParseUserMachineRef(projectSyntax, admin.Project)
	if err != nil {
		return machineTarget{}, err
	}
	if projectRef.Tenant != "" {
		currentTenant = projectRef.Tenant
		explicitTenant = true
	}
	tenants, err := tenant.List(ctx, tenantFilteredStore(store, currentTenant))
	if err != nil {
		return machineTarget{}, err
	}
	summary, ok := findTenantSummary(tenants, currentTenant)
	if !ok {
		return machineTarget{}, fmt.Errorf("Sandcastle tenant %s not found", currentTenant)
	}
	if summary.Version == 2 {
		return machineTarget{}, errV2TenantUnsupported(summary.Tenant)
	}
	return machineTarget{
		Summary:         summary,
		Project:         projectRef.Project,
		Name:            machineName,
		ExplicitTenant:  explicitTenant,
		ExplicitProject: isExplicitProjectSyntax(projectSyntax) || strings.TrimSpace(admin.Project) != "",
		ProjectSyntax:   projectSyntax,
	}, nil
}

func explicitTenantProjectMachineReference(reference string) (string, string, bool) {
	left, right, ok := strings.Cut(strings.TrimSpace(reference), "/")
	if !ok || strings.Contains(left, ":") || strings.Contains(right, "/") || strings.TrimSpace(right) == "" {
		return "", "", false
	}
	if strings.Contains(right, ":") {
		return strings.TrimSpace(left), strings.TrimSpace(right), true
	}
	return "", "", false
}

func possibleTenantMachineReference(reference string) (string, string, bool) {
	left, right, ok := strings.Cut(strings.TrimSpace(reference), "/")
	if !ok || strings.Contains(left, ":") || strings.Contains(right, "/:") || strings.TrimSpace(right) == "" {
		return "", "", false
	}
	return strings.TrimSpace(left), strings.TrimSpace(right), true
}

func tryParseTenantMachineTarget(ctx context.Context, admin config.Admin, store tenant.IncusTenantStore, tenantName string, machineName string) (machineTarget, bool, error) {
	tenantName = strings.TrimSpace(tenantName)
	if tenantName == "" {
		return machineTarget{}, false, nil
	}
	if err := naming.ValidateTenantName(tenantName); err != nil {
		return machineTarget{}, false, nil
	}
	tenants, err := tenant.List(ctx, tenantFilteredStore(store, tenantName))
	if err != nil {
		return machineTarget{}, false, err
	}
	if _, ok := findTenantSummary(tenants, tenantName); !ok {
		return machineTarget{}, false, nil
	}
	target, err := parseMachineTargetInTenant(ctx, admin, tenantListMemoryStore(tenants), tenantName, machineName, true)
	return target, true, err
}

type tenantFilterableStore interface {
	WithTenantFilter(...string) tenant.IncusTenantStore
}

func tenantFilteredStore(store tenant.IncusTenantStore, tenantName string) tenant.IncusTenantStore {
	if filterable, ok := store.(tenantFilterableStore); ok {
		return filterable.WithTenantFilter(tenantName)
	}
	return store
}

func tenantListMemoryStore(summaries []tenant.Summary) tenant.MemoryStore {
	projects := make([]tenant.IncusProject, 0, len(summaries))
	for _, summary := range summaries {
		projects = append(projects, tenant.IncusProject{Name: summary.IncusName, Config: summaryConfig(summary)})
	}
	return tenant.MemoryStore{Projects: projects}
}

func summaryConfig(summary tenant.Summary) map[string]string {
	config, err := meta.TenantConfig(meta.Tenant{
		Tenant:        summary.Tenant,
		Personal:      summary.Personal,
		UnixUser:      summary.UnixUser,
		PrivateCIDR:   summary.PrivateCIDR,
		Projects:      append([]meta.Project{}, summary.Projects...),
		SSHPublicKey:  summary.SSHPublicKey,
		Tailscale:     summary.Tailscale,
		PublicRoutes:  append([]meta.PublicRoute{}, summary.PublicRoutes...),
		StorageShares: append([]meta.TenantStorageShare{}, summary.StorageShares...),
	})
	if err != nil {
		return nil
	}
	return config
}

func findTenantSummary(tenants []tenant.Summary, name string) (tenant.Summary, bool) {
	for _, summary := range tenants {
		if summary.Tenant == name {
			return summary, true
		}
	}
	return tenant.Summary{}, false
}

func isExplicitProjectSyntax(reference string) bool {
	return strings.Contains(reference, "/") || strings.Contains(reference, ":")
}

func findTenant(ctx context.Context, store tenant.IncusTenantStore, ref naming.TenantRef) (tenant.Summary, error) {
	tenants, err := tenant.List(ctx, store)
	if err != nil {
		return tenant.Summary{}, err
	}
	for _, summary := range tenants {
		if summary.Tenant == ref.Tenant {
			if summary.Version == 2 {
				return tenant.Summary{}, errV2TenantUnsupported(summary.Tenant)
			}
			return summary, nil
		}
	}
	return tenant.Summary{}, fmt.Errorf("Sandcastle tenant %s not found", ref.String())
}

// errV2TenantUnsupported keeps the v1 machine-plan machinery (connect, delete,
// restart, port, …) from proceeding on a v2 tenant: its freeform instances use
// plain names, not the v1 <project>-<machine> convention, so v1 plans would
// target instances that don't exist.
func errV2TenantUnsupported(tenantName string) error {
	return fmt.Errorf("tenant %s is a v2 tenant; this command is not v2-aware yet — use `sc create`, `sc list`, or `sc incus` (freeform incus) instead", tenantName)
}

func tenantHasProject(summary tenant.Summary, projectName string) bool {
	for _, candidate := range summary.Projects {
		if candidate.Name == projectName {
			return true
		}
	}
	return false
}

func listExistingMachines(ctx context.Context, store Store, summary tenant.Summary) ([]meta.Machine, error) {
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
