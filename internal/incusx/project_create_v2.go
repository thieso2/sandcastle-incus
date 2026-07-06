package incusx

import (
	"context"
	"fmt"

	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

// CreateProjectV2Result reports the Incus project created for a v2 tenant
// project, plus the shared settings inherited from the tenant's infra project.
type CreateProjectV2Result struct {
	Tenant       string `json:"tenant"`
	Project      string `json:"project"`
	IncusProject string `json:"incusProject"`
	Bridge       string `json:"bridge"`
	DNSSuffix    string `json:"dnsSuffix"`
}

// CreateProjectV2 creates a new app Incus project (sc2-<tenant>-<project>) for
// an existing v2 tenant, wiring it to the shared bridge and installing the
// tenant's default profile (shared-bridge NIC + cloud-init login). It reads the
// tenant's shared settings from the infra project metadata, so the caller only
// supplies tenant + project names. This is the scaffolding the Sandcastle Broker
// performs on a tenant's `sc project create` (ADR-0016); it does not itself
// extend the tenant's restricted cert (the broker/admin layer does that).
func (c TenantCreator) CreateProjectV2(ctx context.Context, tenantName string, project string) (CreateProjectV2Result, error) {
	if err := naming.ValidateTenantName(tenantName); err != nil {
		return CreateProjectV2Result{}, err
	}
	if err := naming.ValidateNewProjectName(project); err != nil {
		return CreateProjectV2Result{}, err
	}
	server, err := c.resolveV2Server()
	if err != nil {
		return CreateProjectV2Result{}, err
	}
	infraProject, err := naming.V2TenantInfraProjectName(naming.V2IncusProjectPrefix, tenantName)
	if err != nil {
		return CreateProjectV2Result{}, err
	}
	infra, _, err := server.GetProject(infraProject)
	if err != nil {
		return CreateProjectV2Result{}, fmt.Errorf("tenant %q infra project %s not found: %w", tenantName, infraProject, err)
	}
	cfg := infra.Config
	prefix := cfg[keyV2Prefix]
	if prefix == "" {
		prefix = naming.V2IncusProjectPrefix
	}
	incusProject, err := naming.V2ProjectName(prefix, tenantName, project)
	if err != nil {
		return CreateProjectV2Result{}, err
	}

	c.log("ensure app project " + incusProject)
	if err := ensureV2Project(server, incusProject, "Sandcastle v2 project "+project+" for "+tenantName, "project", tenantName, true, nil); err != nil {
		return CreateProjectV2Result{}, err
	}
	profilePlan := tenant.CreatePlanV2{
		Tenant:             tenantName,
		DefaultProject:     incusProject,
		Bridge:             cfg[keyV2Bridge],
		StoragePool:        cfg[keyV2Pool],
		DefaultProfileUser: cfg[keyV2User],
		SSHPublicKey:       cfg[keyV2SSHKey],
		DNSSuffix:          cfg[keyV2Suffix],
	}
	if profilePlan.DefaultProfileUser == "" {
		profilePlan.DefaultProfileUser = tenant.DefaultV2UnixUser
	}
	// The profile below references the project's shared volumes, so they must
	// exist first (previously only tenant create made them — a fresh app
	// project's profile pointed at volumes that were never created).
	c.log("ensure shared /workspace + /home volumes in " + incusProject)
	if err := ensureV2ProjectVolumes(server.UseProject(incusProject), profilePlan.StoragePool, tenantName, server.SupportsIdmappedMounts()); err != nil {
		return CreateProjectV2Result{}, err
	}
	c.log("ensure app default profile " + incusProject)
	if err := ensureV2AppProfile(server.UseProject(incusProject), profilePlan, server.SupportsIdmappedMounts(), project); err != nil {
		return CreateProjectV2Result{}, err
	}
	return CreateProjectV2Result{
		Tenant:       tenantName,
		Project:      project,
		IncusProject: incusProject,
		Bridge:       cfg[keyV2Bridge],
		DNSSuffix:    cfg[keyV2Suffix],
	}, nil
}
