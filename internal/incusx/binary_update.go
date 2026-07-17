package incusx

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/lxc/incus/v6/shared/api"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/projectbroker"
)

// SandcastleBinaryPath is where the fat binary lives inside every appliance
// and sidecar (deployed as sandcastle-admin).
const SandcastleBinaryPath = "/usr/local/bin/sandcastle-admin"

// TLSSignUnit is the sidecar's leaf-signer service — the only unit a sidecar
// update restarts (sub-second; CoreDNS/tailscaled untouched, #124 §5).
const TLSSignUnit = "sandcastle-tls-sign.service"

// ComponentVersion is one row of the fleet version table (#124 §4): a
// binary-carrying instance and the version stamped at its last binary push.
type ComponentVersion struct {
	Kind          string `json:"kind"` // auth-app | broker | sidecar
	Project       string `json:"project"`
	Instance      string `json:"instance"`
	Tenant        string `json:"tenant,omitempty"` // sidecars: owning tenant
	BinaryVersion string `json:"binaryVersion"`    // "" = unknown ⇒ outdated
	Status        string `json:"status"`           // Incus instance status
	TenantManaged bool   `json:"tenantManaged"`    // sidecars: tenant updates via sc update
}

// ListBinaryVersions reads every binary-carrying component's stamp in one
// all-projects Incus listing — cheap, and it works for stopped instances.
func (c TenantCreator) ListBinaryVersions() ([]ComponentVersion, error) {
	server, err := c.resolveV2Server()
	if err != nil {
		return nil, err
	}
	instances, err := server.GetInstancesFullAllProjects(api.InstanceTypeAny)
	if err != nil {
		return nil, fmt.Errorf("list instances across projects: %w", err)
	}
	return classifyComponents(instances), nil
}

// classifyComponents filters an all-projects instance listing down to the
// binary-carrying components, ordered auth-app, broker, then sidecars by
// tenant.
func classifyComponents(instances []api.InstanceFull) []ComponentVersion {
	rank := map[string]int{"auth-app": 0, "broker": 1, meta.KindSidecar: 2}
	var components []ComponentVersion
	for _, inst := range instances {
		kind := inst.Config[meta.KeyKind]
		if _, ok := rank[kind]; !ok {
			continue
		}
		components = append(components, ComponentVersion{
			Kind:          kind,
			Project:       inst.Project,
			Instance:      inst.Name,
			Tenant:        inst.Config[meta.KeyTenant],
			BinaryVersion: inst.Config[meta.KeyBinaryVersion],
			Status:        inst.Status,
			TenantManaged: kind == meta.KindSidecar,
		})
	}
	sort.SliceStable(components, func(i, j int) bool {
		if rank[components[i].Kind] != rank[components[j].Kind] {
			return rank[components[i].Kind] < rank[components[j].Kind]
		}
		if components[i].Tenant != components[j].Tenant {
			return components[i].Tenant < components[j].Tenant
		}
		return components[i].Project < components[j].Project
	})
	return components
}

// UpdateApplianceBinary replaces the sandcastle binary inside an existing
// appliance instance (auth-app or broker), restarts its service units, and
// stamps the new version. Idempotent — re-running repairs a partial update.
func (c TenantCreator) UpdateApplianceBinary(project, instance string, binary []byte, binaryVersion string, units ...string) error {
	server, err := c.resolveV2Server()
	if err != nil {
		return err
	}
	psrv := server.UseProject(project)
	if _, _, err := psrv.GetInstance(instance); err != nil {
		return fmt.Errorf("appliance %s/%s: %w", project, instance, err)
	}
	c.log("push binary into " + project + "/" + instance)
	if err := writeApplianceFile(psrv, instance, applianceFile{SandcastleBinaryPath, binary, 0o755}); err != nil {
		return fmt.Errorf("push binary into %s/%s: %w", project, instance, err)
	}
	if err := stampBinaryVersion(psrv, instance, binaryVersion); err != nil {
		return err
	}
	for _, unit := range units {
		c.log("restart " + unit + " in " + instance)
		script := "systemctl restart " + unit + " && sleep 1 && systemctl is-active " + unit
		if err := execSidecar(psrv, instance, script); err != nil {
			return fmt.Errorf("restart %s in %s/%s: %w", unit, project, instance, err)
		}
	}
	return nil
}

// UpdateTenantSidecar pushes the given binary into the tenant's sidecar,
// restarts the TLS leaf signer, verifies it is active, and stamps the
// version. Unlike the create path there is no "binary already exists" skip —
// updates push unconditionally (#124 §7).
func (c TenantCreator) UpdateTenantSidecar(prefix, tenantName string, binary []byte, binaryVersion string) (ComponentVersion, error) {
	infraProject, err := naming.V2TenantInfraProjectName(prefix, tenantName)
	if err != nil {
		return ComponentVersion{}, err
	}
	server, err := c.resolveV2Server()
	if err != nil {
		return ComponentVersion{}, err
	}
	psrv := server.UseProject(infraProject)
	instance := naming.V2SidecarInstanceName
	if _, _, err := psrv.GetInstance(instance); err != nil {
		return ComponentVersion{}, fmt.Errorf("sidecar of tenant %s (%s/%s): %w", tenantName, infraProject, instance, err)
	}
	c.log("push binary into sidecar " + infraProject + "/" + instance)
	if err := writeApplianceFile(psrv, instance, applianceFile{SandcastleBinaryPath, binary, 0o755}); err != nil {
		return ComponentVersion{}, fmt.Errorf("push binary into sidecar %s: %w", infraProject, err)
	}
	if err := stampBinaryVersion(psrv, instance, binaryVersion); err != nil {
		return ComponentVersion{}, err
	}
	c.log("restart " + TLSSignUnit)
	script := "systemctl restart " + TLSSignUnit + " && sleep 1 && systemctl is-active " + TLSSignUnit
	if err := execSidecar(psrv, instance, script); err != nil {
		return ComponentVersion{}, fmt.Errorf("restart %s on sidecar of %s: %w", TLSSignUnit, tenantName, err)
	}
	version := binaryVersion
	if version != "" && !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return ComponentVersion{
		Kind:          meta.KindSidecar,
		Project:       infraProject,
		Instance:      instance,
		Tenant:        tenantName,
		BinaryVersion: version,
		Status:        "Running",
		TenantManaged: true,
	}, nil
}

// SidecarSelfUpdater implements the broker/auth-app side of the delegated
// tenant sidecar update (#124 §5): it pushes its OWN running binary
// (os.Executable) into the tenant's sidecar — so sidecars can never run
// ahead of the deployment, and no GitHub access is needed on this path.
type SidecarSelfUpdater struct {
	Creator TenantCreator
	Admin   config.Admin
}

// UpdateTenantSidecar satisfies projectbroker.SidecarUpdater (also used by
// the auth-app's token-authenticated variant).
func (u SidecarSelfUpdater) UpdateTenantSidecar(tenantName string) (projectbroker.SidecarUpdateResult, error) {
	exe, err := os.Executable()
	if err != nil {
		return projectbroker.SidecarUpdateResult{}, fmt.Errorf("locate running binary: %w", err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		return projectbroker.SidecarUpdateResult{}, fmt.Errorf("read running binary: %w", err)
	}
	component, err := u.Creator.UpdateTenantSidecar(v2Prefix(u.Admin.IncusProjectPrefix), tenantName, binary, runningBinaryVersion)
	if err != nil {
		return projectbroker.SidecarUpdateResult{}, err
	}
	return projectbroker.SidecarUpdateResult{
		Tenant:        component.Tenant,
		Project:       component.Project,
		Instance:      component.Instance,
		BinaryVersion: component.BinaryVersion,
	}, nil
}

// v2Prefix normalizes the installation prefix the way tenant.PlanCreateV2
// does: empty or the legacy default fall back to the v2 default.
func v2Prefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || prefix == naming.DefaultIncusProjectPrefix {
		return naming.V2IncusProjectPrefix
	}
	return prefix
}
