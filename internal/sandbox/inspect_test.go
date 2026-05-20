package sandbox

import (
	"context"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
)

func TestInspectFindsManagedSandbox(t *testing.T) {
	projectStore := projectStoreForTest(t)
	result, err := Inspect(context.Background(), config.LoadAdminFromEnv(), projectStore, fakeSandboxStore{sandboxes: []meta.Sandbox{{
		Owner:        "alice",
		Project:      "myproject",
		Name:         "codex",
		AppPort:      5173,
		PrivateIP:    "10.248.0.20",
		HomeDir:      ".",
		WorkspaceDir: "workspace",
		Running:      true,
	}}}, InspectRequest{Reference: "alice/myproject/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.InstanceName != "sc-codex" {
		t.Fatalf("InstanceName = %q", result.InstanceName)
	}
	if result.Sandbox.AppPort != 5173 || result.Sandbox.PrivateIP != "10.248.0.20" {
		t.Fatalf("Sandbox = %#v", result.Sandbox)
	}
	if !result.Sandbox.Running {
		t.Fatal("expected running state from sandbox store")
	}
}

func TestInspectSupportsProjectNameShorthandWithOwner(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Owner = "alice"
	result, err := Inspect(context.Background(), admin, projectStoreForTest(t), fakeSandboxStore{sandboxes: []meta.Sandbox{{
		Owner:     "alice",
		Project:   "myproject",
		Name:      "codex",
		AppPort:   5173,
		PrivateIP: "10.248.0.20",
	}}}, InspectRequest{Reference: "myproject/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Project.Owner != "alice" || result.Project.Name != "myproject" || result.Name != "codex" {
		t.Fatalf("result = %#v", result)
	}
}

func TestInspectErrorsForMissingSandbox(t *testing.T) {
	_, err := Inspect(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), fakeSandboxStore{}, InspectRequest{Reference: "alice/myproject/missing"})
	if err == nil {
		t.Fatal("expected error")
	}
}
