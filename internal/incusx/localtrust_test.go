package incusx

import (
	"context"
	"io"
	"strings"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/thieso2/sandcastle-incus/internal/localtrust"
)

type fakeLocalTrustServer struct {
	project *fakeLocalTrustProjectServer
}

func (s *fakeLocalTrustServer) UseProject(name string) LocalTrustProjectServer {
	s.project.project = name
	return s.project
}

type fakeLocalTrustProjectServer struct {
	project string
	files   map[string]string
}

func (s *fakeLocalTrustProjectServer) GetStorageVolumeFile(pool string, volumeType string, volumeName string, filePath string) (io.ReadCloser, *incus.InstanceFileResponse, error) {
	return io.NopCloser(strings.NewReader(s.files[volumeName+":"+filePath])), nil, nil
}

type fakeLocalTrustStore struct {
	installed bool
	removed   bool
	certPEM   string
}

func (s *fakeLocalTrustStore) InstallCA(ctx context.Context, plan localtrust.Plan, certPEM []byte) (localtrust.Result, error) {
	s.installed = true
	s.certPEM = string(certPEM)
	return localtrust.Result{Reference: plan.Reference, TrustName: plan.TrustName, Action: "install", Platform: "fake"}, nil
}

func (s *fakeLocalTrustStore) UninstallCA(ctx context.Context, plan localtrust.Plan) (localtrust.Result, error) {
	s.removed = true
	return localtrust.Result{Reference: plan.Reference, TrustName: plan.TrustName, Action: "uninstall", Platform: "fake"}, nil
}

func TestLocalTrustManagerReadsProjectCAForInstall(t *testing.T) {
	projectServer := &fakeLocalTrustProjectServer{files: map[string]string{"sc-ca:/ca.crt": "CERT"}}
	store := &fakeLocalTrustStore{}
	manager := LocalTrustManager{
		Server: &fakeLocalTrustServer{project: projectServer},
		Store:  store,
	}
	result, err := manager.Install(context.Background(), localtrust.Plan{
		Reference:       "alice/myproject",
		IncusProject:    "sc-alice-myproject",
		StoragePool:     "default",
		CAVolume:        "sc-ca",
		CertificatePath: "/ca.crt",
		TrustName:       "Sandcastle alice/myproject project CA",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "install" {
		t.Fatalf("Action = %q", result.Action)
	}
	if projectServer.project != "sc-alice-myproject" {
		t.Fatalf("project = %q", projectServer.project)
	}
	if store.certPEM != "CERT" {
		t.Fatalf("certPEM = %q", store.certPEM)
	}
}

func TestLocalTrustManagerUninstallSkipsIncusRead(t *testing.T) {
	store := &fakeLocalTrustStore{}
	manager := LocalTrustManager{Store: store}
	result, err := manager.Uninstall(context.Background(), localtrust.Plan{
		Reference: "alice/myproject",
		TrustName: "Sandcastle alice/myproject project CA",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "uninstall" {
		t.Fatalf("Action = %q", result.Action)
	}
	if !store.removed {
		t.Fatal("expected store uninstall call")
	}
}
