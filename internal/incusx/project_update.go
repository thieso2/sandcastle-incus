package incusx

import (
	"context"
	"fmt"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/meta"
)

type ProjectSSHKeyManager struct {
	Remote     string
	ConfigPath string
}

func NewProjectSSHKeyManager(remote string) ProjectSSHKeyManager {
	return ProjectSSHKeyManager{Remote: remote}
}

func (m ProjectSSHKeyManager) SetProjectSSHKey(_ context.Context, incusProjectName string, sshKey string) error {
	loaded, err := cliconfig.LoadConfig(m.ConfigPath)
	if err != nil {
		return fmt.Errorf("load Incus config: %w", err)
	}
	remote := m.Remote
	if remote == "" {
		remote = loaded.DefaultRemote
	}
	server, err := loaded.GetInstanceServer(remote)
	if err != nil {
		return fmt.Errorf("connect to Incus remote %q: %w", remote, err)
	}
	incusProject, etag, err := server.GetProject(incusProjectName)
	if err != nil {
		return fmt.Errorf("get project %s: %w", incusProjectName, err)
	}
	managed, err := meta.ParseProjectConfig(map[string]string(incusProject.Config))
	if err != nil {
		return fmt.Errorf("parse project metadata for %s: %w", incusProjectName, err)
	}
	managed.SSHPublicKey = sshKey
	config, err := meta.ProjectConfig(managed)
	if err != nil {
		return err
	}
	put := incusProject.Writable()
	if put.Config == nil {
		put.Config = api.ConfigMap{}
	}
	for key, value := range config {
		put.Config[key] = value
	}
	if err := server.UpdateProject(incusProjectName, put, etag); err != nil {
		return fmt.Errorf("update project %s: %w", incusProjectName, err)
	}
	return nil
}
