package incusx

import (
	"context"
	"fmt"
	"io"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/cliconfig"
	"github.com/thieso2/sandcastle-incus/internal/localtrust"
)

type LocalTrustServer interface {
	UseProject(name string) LocalTrustTenantResourceServer
}

type LocalTrustTenantResourceServer interface {
	GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error)
}

type LocalTrustManager struct {
	Remote     string
	ConfigPath string
	Server     LocalTrustServer
	Store      localtrust.Store
}

func NewLocalTrustManager(remote string, store localtrust.Store) LocalTrustManager {
	return LocalTrustManager{Remote: remote, Store: store}
}

func (m LocalTrustManager) Install(ctx context.Context, plan localtrust.Plan) (localtrust.Result, error) {
	certPEM, err := m.readCA(plan)
	if err != nil {
		return localtrust.Result{}, err
	}
	store := m.Store
	if store == nil {
		store = localtrust.NewPlatformStore()
	}
	return store.InstallCA(ctx, plan, certPEM)
}

func (m LocalTrustManager) Uninstall(ctx context.Context, plan localtrust.Plan) (localtrust.Result, error) {
	store := m.Store
	if store == nil {
		store = localtrust.NewPlatformStore()
	}
	return store.UninstallCA(ctx, plan)
}

func (m LocalTrustManager) readCA(plan localtrust.Plan) ([]byte, error) {
	server := m.Server
	if server == nil {
		loaded, err := cliconfig.LoadConfig(m.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("load Incus config: %w", err)
		}
		remote := m.Remote
		if remote == "" {
			remote = loaded.DefaultRemote
		}
		instanceServer, err := loaded.GetInstanceServer(remote)
		if err != nil {
			return nil, fmt.Errorf("connect to Incus remote %q: %w", remote, err)
		}
		server = sdkLocalTrustServer{inner: instanceServer}
	}
	projectServer := server.UseProject(plan.IncusProject)
	content, _, err := projectServer.GetStorageVolumeFile(plan.StoragePool, "custom", plan.CAVolume, plan.CertificatePath)
	if err != nil {
		return nil, fmt.Errorf("read tenant CA certificate: %w", err)
	}
	defer content.Close()
	return io.ReadAll(content)
}

type sdkLocalTrustServer struct {
	inner incus.InstanceServer
}

func (s sdkLocalTrustServer) UseProject(name string) LocalTrustTenantResourceServer {
	return sdkLocalTrustTenantResourceServer{inner: s.inner.UseProject(name), projectName: name}
}

type sdkLocalTrustTenantResourceServer struct {
	inner       incus.InstanceServer
	projectName string
}

func (s sdkLocalTrustTenantResourceServer) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	return getStorageVolumeFile(s.inner, s.projectName, pool, volumeType, volumeName, filePath)
}
