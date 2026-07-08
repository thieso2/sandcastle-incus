package incusx

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/lxc/incus/v6/shared/api"
)

// v2SaveSnapshotName is the transient instance snapshot `sc image save` creates
// so it can publish a running machine without stopping it. It is deleted as soon
// as the image is published.
const v2SaveSnapshotName = "sc-save"

// SavedImage describes a reusable base image a tenant published from one of its
// machines with `sc image save`.
type SavedImage struct {
	Name        string    `json:"name"`
	Fingerprint string    `json:"fingerprint"`
	Size        int64     `json:"size"`
	CreatedAt   time.Time `json:"createdAt"`
	Source      string    `json:"source,omitempty"`
	Description string    `json:"description,omitempty"`
}

// SaveMachineImageV2 snapshots a running v2 machine and publishes the snapshot as
// a reusable local image aliased baseName in the tenant's app project. The
// machine keeps running (we publish a transient snapshot, not the live rootfs),
// and the tenant's shared /home and /workspace custom volumes are excluded — they
// are attached devices, not part of the instance rootfs, so the image is purely
// the installed-software layer. Re-saving the same name replaces the prior image.
func (c TenantCreator) SaveMachineImageV2(ctx context.Context, incusProject, machine, baseName string) (SavedImage, error) {
	server, err := c.resolveV2Server()
	if err != nil {
		return SavedImage{}, err
	}
	project := server.UseProject(incusProject)
	if _, _, err := project.GetInstance(machine); err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return SavedImage{}, fmt.Errorf("machine %q not found in project %s", machine, incusProject)
		}
		return SavedImage{}, fmt.Errorf("get machine %s: %w", machine, err)
	}

	// Reuse: drop an existing alias + its image so re-saving a name is idempotent.
	if err := deleteImageByAlias(project, baseName); err != nil {
		return SavedImage{}, err
	}

	c.log("snapshotting " + machine + " for image " + baseName)
	if err := replaceInstanceSnapshot(project, machine, v2SaveSnapshotName); err != nil {
		return SavedImage{}, err
	}
	defer func() {
		if op, derr := project.DeleteInstanceSnapshot(machine, v2SaveSnapshotName); derr == nil {
			_ = op.Wait()
		}
	}()

	c.log("publishing image " + baseName + " from " + machine + "/" + v2SaveSnapshotName)
	op, err := project.CreateImage(api.ImagesPost{
		Filename: baseName,
		Source: &api.ImagesPostSource{
			Type: "snapshot",
			Name: machine + "/" + v2SaveSnapshotName,
		},
		ImagePut: api.ImagePut{
			Public:   false,
			Profiles: []string{"default"},
			Properties: map[string]string{
				"sandcastle.base":   baseName,
				"sandcastle.source": machine,
				"description":       "Sandcastle base image " + baseName + " (from " + machine + ")",
			},
		},
		Aliases: []api.ImageAlias{{Name: baseName}},
	}, nil)
	if err != nil {
		return SavedImage{}, fmt.Errorf("publish image %s: %w", baseName, err)
	}
	if err := op.Wait(); err != nil {
		return SavedImage{}, fmt.Errorf("wait for image %s publish: %w", baseName, err)
	}

	saved := SavedImage{Name: baseName, Source: machine}
	if alias, _, err := project.GetImageAlias(baseName); err == nil {
		saved.Fingerprint = alias.Target
		if img, _, err := project.GetImage(alias.Target); err == nil {
			saved.Size = img.Size
			saved.CreatedAt = img.CreatedAt
			saved.Description = img.Properties["description"]
		}
	}
	return saved, nil
}

// ListImagesV2 returns the tenant's saved base images in the given app project.
// Only aliased images (the ones `sc image save` creates) are surfaced.
func (c TenantCreator) ListImagesV2(incusProject string) ([]SavedImage, error) {
	server, err := c.resolveV2Server()
	if err != nil {
		return nil, err
	}
	project := server.UseProject(incusProject)
	images, err := project.GetImages()
	if err != nil {
		return nil, fmt.Errorf("list images in %s: %w", incusProject, err)
	}
	saved := make([]SavedImage, 0, len(images))
	for _, img := range images {
		if len(img.Aliases) == 0 {
			continue
		}
		saved = append(saved, SavedImage{
			Name:        img.Aliases[0].Name,
			Fingerprint: img.Fingerprint,
			Size:        img.Size,
			CreatedAt:   img.CreatedAt,
			Source:      img.Properties["sandcastle.source"],
			Description: img.Properties["description"],
		})
	}
	sort.Slice(saved, func(i, j int) bool { return saved[i].Name < saved[j].Name })
	return saved, nil
}

// DeleteImageV2 removes a saved base image (alias + underlying image) from the
// tenant's app project.
func (c TenantCreator) DeleteImageV2(incusProject, baseName string) error {
	server, err := c.resolveV2Server()
	if err != nil {
		return err
	}
	project := server.UseProject(incusProject)
	alias, _, err := project.GetImageAlias(baseName)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return fmt.Errorf("image %q not found in project %s", baseName, incusProject)
		}
		return fmt.Errorf("look up image %s: %w", baseName, err)
	}
	_ = project.DeleteImageAlias(baseName)
	op, err := project.DeleteImage(alias.Target)
	if err != nil {
		return fmt.Errorf("delete image %s: %w", baseName, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for image %s deletion: %w", baseName, err)
	}
	return nil
}

// deleteImageByAlias removes an alias and its image if present; absent is fine.
func deleteImageByAlias(project TenantResourceServer, name string) error {
	alias, _, err := project.GetImageAlias(name)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}
		return fmt.Errorf("look up image %s: %w", name, err)
	}
	_ = project.DeleteImageAlias(name)
	if op, err := project.DeleteImage(alias.Target); err == nil {
		_ = op.Wait()
	}
	return nil
}

// replaceInstanceSnapshot creates a fresh snapshot, deleting any stale one of the
// same name first (a prior save that failed before cleanup).
func replaceInstanceSnapshot(project TenantResourceServer, machine, snapshot string) error {
	if op, err := project.DeleteInstanceSnapshot(machine, snapshot); err == nil {
		_ = op.Wait()
	}
	op, err := project.CreateInstanceSnapshot(machine, api.InstanceSnapshotsPost{Name: snapshot, Stateful: false})
	if err != nil {
		return fmt.Errorf("snapshot %s: %w", machine, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("wait for snapshot %s: %w", machine, err)
	}
	return nil
}
