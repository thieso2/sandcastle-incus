package incusx

import (
	"context"
	"fmt"
	"strings"

	"github.com/lxc/incus/v6/shared/api"

	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

// SetTenantSSHKeyV2 rotates the SSH public key baked into a v2 tenant's machines.
//
// The authoritative store is the infra project's `user.sandcastle.v2.sshkey`
// config — that is what `ensureV2AppProfile` renders into each app project's
// default profile cloud-init, and therefore what a newly created machine
// authorizes. Writing the key into the tenant's `workspace` metadata file (what
// the old code did) reached nothing: no code reads that file back.
//
// Existing machines keep the key they were created with; cloud-init only runs
// once. Rotating for a running machine is `MachineSSHKeyReconciler`'s job.
func (c TenantCreator) SetTenantSSHKeyV2(_ context.Context, installPrefix string, tenantName string, sshKey string, projects []string) error {
	sshKey = strings.TrimSpace(sshKey)
	if sshKey == "" {
		return fmt.Errorf("ssh key is required")
	}
	if err := naming.ValidateTenantName(tenantName); err != nil {
		return err
	}
	server, err := c.resolveV2Server()
	if err != nil {
		return err
	}
	installPrefix = strings.TrimSpace(installPrefix)
	if installPrefix == "" || installPrefix == naming.DefaultIncusProjectPrefix {
		installPrefix = naming.V2IncusProjectPrefix
	}
	infraProject, err := naming.V2TenantInfraProjectName(installPrefix, tenantName)
	if err != nil {
		return err
	}
	infra, etag, err := server.GetProject(infraProject)
	if err != nil {
		return fmt.Errorf("tenant %q infra project %s not found: %w", tenantName, infraProject, err)
	}
	config := map[string]string{}
	for key, value := range infra.Config {
		config[key] = value
	}
	config[keyV2SSHKey] = sshKey
	c.log("update " + keyV2SSHKey + " on " + infraProject)
	if err := server.UpdateProject(infraProject, api.ProjectPut{Config: config, Description: infra.Description}, etag); err != nil {
		return fmt.Errorf("store ssh key on %s: %w", infraProject, err)
	}

	prefix := config[keyV2Prefix]
	if prefix == "" {
		prefix = naming.V2IncusProjectPrefix
	}
	// Re-render every app project's default profile so machines created from now
	// on authorize the new key. Without this the command changed nothing a
	// machine could observe.
	for _, project := range projects {
		project = strings.TrimSpace(project)
		if project == "" {
			continue
		}
		incusProject, err := naming.V2ProjectName(prefix, tenantName, project)
		if err != nil {
			return err
		}
		plan := tenant.CreatePlanV2{
			Tenant:             tenantName,
			DefaultProject:     incusProject,
			Bridge:             config[keyV2Bridge],
			StoragePool:        config[keyV2Pool],
			DefaultProfileUser: config[keyV2User],
			SSHPublicKey:       sshKey,
			DNSSuffix:          config[keyV2Suffix],
		}
		if plan.DefaultProfileUser == "" {
			plan.DefaultProfileUser = tenant.DefaultV2UnixUser
		}
		c.log("re-render default profile of " + incusProject)
		if err := ensureV2AppProfile(server.UseProject(incusProject), plan, server.SupportsIdmappedMounts(), project); err != nil {
			return fmt.Errorf("update default profile of %s: %w", incusProject, err)
		}
	}
	return nil
}
