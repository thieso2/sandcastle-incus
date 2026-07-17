package incusx

import (
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	incus "github.com/lxc/incus/v6/client"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/tenant"
)

// SCPayloadProjectStatus reports one app project's /.sc platform-payload state
// from a central sync: the version found on the volume, the version this
// binary ships, and whether the sync (re)wrote the payload.
type SCPayloadProjectStatus struct {
	IncusProject string `json:"incusProject"`
	Before       string `json:"before"`
	Target       string `json:"target"`
	Changed      bool   `json:"changed"`
}

// SyncTenantPlatformPayload converges every app project of a tenant onto the
// platform payload built into this binary — the central update path (#131):
// one volume write per project, never per machine; every running machine
// observes the new payload through its shared /.sc mount, no re-create needed.
// Rollback is the same operation run from the previous binary (the payload —
// and its content-derived version — comes from the binary). With checkOnly the
// volumes are only read, reporting drift per project.
func (c TenantCreator) SyncTenantPlatformPayload(_ context.Context, installPrefix string, tenantName string, checkOnly bool) ([]SCPayloadProjectStatus, error) {
	if err := naming.ValidateTenantName(tenantName); err != nil {
		return nil, err
	}
	server, err := c.resolveV2Server()
	if err != nil {
		return nil, err
	}
	installPrefix = strings.TrimSpace(installPrefix)
	if installPrefix == "" || installPrefix == naming.DefaultIncusProjectPrefix {
		installPrefix = naming.V2IncusProjectPrefix
	}
	infraProject, err := naming.V2TenantInfraProjectName(installPrefix, tenantName)
	if err != nil {
		return nil, err
	}
	infra, _, err := server.GetProject(infraProject)
	if err != nil {
		return nil, fmt.Errorf("tenant %q infra project %s not found: %w", tenantName, infraProject, err)
	}
	pool := strings.TrimSpace(infra.Config[keyV2Pool])
	if pool == "" {
		pool = "default"
	}
	names, err := server.GetProjectNames()
	if err != nil {
		return nil, fmt.Errorf("list Incus projects: %w", err)
	}
	target := tenant.PlatformPayloadVersion()
	statuses := make([]SCPayloadProjectStatus, 0, 2)
	for _, name := range names {
		// This install's app projects only: name-scoped to the tenant AND
		// metadata-confirmed (a same-named tenant of another install has a
		// different prefix; a non-Sandcastle project fails the kind check).
		if !strings.HasPrefix(name, infraProject+"-") {
			continue
		}
		project, _, err := server.GetProject(name)
		if err != nil {
			return statuses, fmt.Errorf("get project %s: %w", name, err)
		}
		if project.Config[meta.KeyKind] != "project" || project.Config[meta.KeyTenant] != tenantName {
			continue
		}
		resource := server.UseProject(name)
		status := SCPayloadProjectStatus{
			IncusProject: name,
			Before:       readSCPlatformVersion(resource, pool),
			Target:       target,
		}
		if !checkOnly && status.Before != target {
			changed, err := ensureV2PlatformPayload(resource, pool)
			if err != nil {
				return statuses, fmt.Errorf("sync /.sc payload in %s: %w", name, err)
			}
			status.Changed = changed
		}
		statuses = append(statuses, status)
	}
	if len(statuses) == 0 {
		return nil, fmt.Errorf("tenant %q has no app projects under install prefix %q", tenantName, installPrefix)
	}
	return statuses, nil
}

// EnsureProjectPlatformPayload converges ONE app project's sc-platform volume
// onto this binary's payload — the central half of `sc fix` (#132). The
// tenant's own restricted client certificate may perform it: the volume lives
// inside their project, and platform read-only-ness is a machine-mount
// property, not an API one. The storage pool is read from the project's
// default profile (its sc-platform disk device), so the tenant needs no admin
// configuration. With checkOnly the volume is only read.
func (c TenantCreator) EnsureProjectPlatformPayload(_ context.Context, incusProject string, checkOnly bool) (SCPayloadProjectStatus, error) {
	server, err := c.resolveV2Server()
	if err != nil {
		return SCPayloadProjectStatus{}, err
	}
	resource := server.UseProject(incusProject)
	profile, _, err := resource.GetProfile("default")
	if err != nil {
		return SCPayloadProjectStatus{}, fmt.Errorf("get default profile of %s: %w", incusProject, err)
	}
	pool := ""
	for _, v := range tenant.V2SCVolumes() {
		if device, ok := profile.Devices[v.DeviceName]; ok && v.Volume == tenant.V2SCPlatformVolumeName {
			pool = device["pool"]
		}
	}
	if pool == "" {
		return SCPayloadProjectStatus{}, fmt.Errorf("project %s has no /.sc volumes yet — re-run `sc login` to provision them (or have the admin run `sc-adm tenant payload-sync`)", incusProject)
	}
	status := SCPayloadProjectStatus{
		IncusProject: incusProject,
		Before:       readSCPlatformVersion(resource, pool),
		Target:       tenant.PlatformPayloadVersion(),
	}
	if !checkOnly && status.Before != status.Target {
		changed, err := ensureV2PlatformPayload(resource, pool)
		if err != nil {
			return status, fmt.Errorf("sync /.sc payload in %s: %w", incusProject, err)
		}
		status.Changed = changed
	}
	return status, nil
}

// ensureV2PlatformPayload converges one app project's sc-platform volume onto
// the platform payload built into this binary (ADR-0022). This is the central
// update path: one write per project, never per machine — every machine mounts
// the shared volume, so the guarded shims pick the new payload up on next use.
// Idempotent: a matching VERSION marker is a no-op. Returns whether anything
// was written.
func ensureV2PlatformPayload(server TenantResourceServer, pool string) (bool, error) {
	files, version := tenant.PlatformPayload()
	if current := readSCPlatformVersion(server, pool); current == version {
		return false, nil
	}
	// Parent directories first (the volume file API does not create them).
	dirs := map[string]bool{}
	for _, f := range files {
		for d := path.Dir(f.Path); d != "." && d != "/"; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	ordered := make([]string, 0, len(dirs))
	for d := range dirs {
		ordered = append(ordered, d)
	}
	sort.Strings(ordered) // parents sort before children
	for _, d := range ordered {
		if err := server.CreateStorageVolumeFile(pool, "custom", tenant.V2SCPlatformVolumeName, "/"+d, incus.InstanceFileArgs{
			Type: "directory",
			Mode: 0o755,
		}); err != nil && !strings.Contains(err.Error(), "already exists") {
			return false, fmt.Errorf("create /.sc/platform directory %s: %w", d, err)
		}
	}
	// The builder returns VERSION last, so a partially applied write never
	// advertises the new version — the next sync retries.
	for _, f := range files {
		if err := server.CreateStorageVolumeFile(pool, "custom", tenant.V2SCPlatformVolumeName, "/"+f.Path, incus.InstanceFileArgs{
			Content:   strings.NewReader(f.Content),
			Type:      "file",
			Mode:      f.Mode,
			WriteMode: "overwrite",
		}); err != nil {
			return false, fmt.Errorf("write /.sc/platform/%s: %w", f.Path, err)
		}
	}
	return true, nil
}

// readSCPlatformVersion reports the payload version currently on a project's
// sc-platform volume ("" when none is written yet or the volume is missing).
func readSCPlatformVersion(server TenantResourceServer, pool string) string {
	content, _, err := server.GetStorageVolumeFile(pool, "custom", tenant.V2SCPlatformVolumeName, "/"+tenant.PlatformPayloadVersionFile)
	if err != nil {
		return ""
	}
	defer content.Close()
	data, err := io.ReadAll(content)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
