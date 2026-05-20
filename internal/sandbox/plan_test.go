package sandbox

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/certs"
	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

func TestPlanCreate(t *testing.T) {
	projectConfig, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-alice-myproject",
		Config: projectConfig,
	}}}, nil, CreateRequest{Reference: "alice/myproject/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.InstanceName != "sc-codex" {
		t.Fatalf("InstanceName = %q", plan.InstanceName)
	}
	if plan.PrivateIP != "10.248.0.20" {
		t.Fatalf("PrivateIP = %q", plan.PrivateIP)
	}
	if plan.AppPort != DefaultAppPort {
		t.Fatalf("AppPort = %d", plan.AppPort)
	}
	if plan.LinuxUser != "alice" {
		t.Fatalf("LinuxUser = %q", plan.LinuxUser)
	}
	if plan.Template != TemplateAI {
		t.Fatalf("Template = %q", plan.Template)
	}
	if plan.ContainerTools {
		t.Fatal("ContainerTools = true, want false")
	}
	if plan.HomeDir != "." || plan.WorkspaceDir != "." {
		t.Fatalf("HomeDir/WorkspaceDir = %q/%q, want ./.", plan.HomeDir, plan.WorkspaceDir)
	}
	if plan.CAVolume != project.CAVolumeName {
		t.Fatalf("CAVolume = %q", plan.CAVolume)
	}
	if plan.MetadataConfig[meta.KeyKind] != meta.KindSandbox {
		t.Fatalf("metadata kind = %q", plan.MetadataConfig[meta.KeyKind])
	}
	if plan.MetadataConfig[meta.KeyLinuxUser] != "alice" {
		t.Fatalf("metadata linux user = %q", plan.MetadataConfig[meta.KeyLinuxUser])
	}
	if plan.Devices["home"]["path"] != "/home/alice" {
		t.Fatalf("home path = %q", plan.Devices["home"]["path"])
	}
	if plan.CaddyFile.Path != CaddyfilePath {
		t.Fatalf("CaddyFile.Path = %q", plan.CaddyFile.Path)
	}
	if !strings.Contains(plan.CaddyFile.Content, "codex.myproject.project-tld") {
		t.Fatalf("CaddyFile.Content = %q", plan.CaddyFile.Content)
	}
	if !strings.Contains(plan.CaddyFile.Content, "reverse_proxy 127.0.0.1:3000") {
		t.Fatalf("CaddyFile.Content = %q", plan.CaddyFile.Content)
	}
	state, err := meta.ParseSandboxConfig(plan.MetadataConfig)
	if err != nil {
		t.Fatal(err)
	}
	if state.ContainerTools {
		t.Fatal("metadata ContainerTools = true, want false")
	}
}

func TestPlanCreateSupportsContainerTools(t *testing.T) {
	plan, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), nil, CreateRequest{
		Reference:      "alice/myproject/codex",
		ContainerTools: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.ContainerTools {
		t.Fatal("ContainerTools = false, want true")
	}
	state, err := meta.ParseSandboxConfig(plan.MetadataConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !state.ContainerTools {
		t.Fatal("metadata ContainerTools = false, want true")
	}
}

func TestPlanCreateSupportsProjectNameShorthandWithOwner(t *testing.T) {
	admin := config.LoadAdminFromEnv()
	admin.Owner = "alice"
	plan, err := PlanCreate(context.Background(), admin, projectStoreForTest(t), nil, CreateRequest{Reference: "myproject/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Reference != "myproject/codex" {
		t.Fatalf("Reference = %q", plan.Reference)
	}
	if plan.Project.Owner != "alice" || plan.Project.Name != "myproject" || plan.Name != "codex" {
		t.Fatalf("plan = %#v", plan)
	}
	if plan.LinuxUser != "alice" || plan.Devices["home"]["path"] != "/home/alice" {
		t.Fatalf("linux user/home path = %q/%q", plan.LinuxUser, plan.Devices["home"]["path"])
	}
}

func TestPlanCreateRejectsProjectNameShorthandWithoutOwner(t *testing.T) {
	_, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), nil, CreateRequest{Reference: "myproject/codex"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "SANDCASTLE_OWNER") {
		t.Fatalf("error = %q", err)
	}
}

func TestPlanCreateSupportsBaseTemplateAndCustomStorageDirs(t *testing.T) {
	plan, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), nil, CreateRequest{
		Reference:    "alice/myproject/minimal",
		Template:     TemplateBase,
		HomeDir:      "shared-home",
		WorkspaceDir: ".",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Template != TemplateBase {
		t.Fatalf("Template = %q", plan.Template)
	}
	if plan.ImageAlias != config.DefaultBaseImageAlias {
		t.Fatalf("ImageAlias = %q, want %q", plan.ImageAlias, config.DefaultBaseImageAlias)
	}
	if plan.Devices["home"]["source"] != project.HomeVolumeName+"/shared-home" {
		t.Fatalf("home source = %q", plan.Devices["home"]["source"])
	}
	if plan.Devices["workspace"]["source"] != project.WorkspaceVolumeName+"/." {
		t.Fatalf("workspace source = %q", plan.Devices["workspace"]["source"])
	}
}

func TestPlanCreateRejectsUnsafeStorageDirs(t *testing.T) {
	for _, tc := range []struct {
		name         string
		homeDir      string
		workspaceDir string
		want         string
	}{
		{name: "absolute home", homeDir: "/alice", want: "home directory"},
		{name: "home traversal", homeDir: "../alice", want: ".. path segments"},
		{name: "absolute workspace", workspaceDir: "/work", want: "workspace directory"},
		{name: "workspace traversal", workspaceDir: "work/../other", want: ".. path segments"},
		{name: "backslash", workspaceDir: `work\other`, want: "forward-slash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), nil, CreateRequest{
				Reference:    "alice/myproject/minimal",
				HomeDir:      tc.homeDir,
				WorkspaceDir: tc.workspaceDir,
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want %q", err, tc.want)
			}
		})
	}
}

func TestPlanCreateNormalizesStorageDirs(t *testing.T) {
	plan, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), nil, CreateRequest{
		Reference:    "alice/myproject/minimal",
		HomeDir:      " dev//home ",
		WorkspaceDir: "workspace/.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.HomeDir != "dev/home" || plan.WorkspaceDir != "workspace" {
		t.Fatalf("HomeDir/WorkspaceDir = %q/%q", plan.HomeDir, plan.WorkspaceDir)
	}
	if plan.Devices["home"]["source"] != project.HomeVolumeName+"/dev/home" {
		t.Fatalf("home source = %q", plan.Devices["home"]["source"])
	}
	if plan.Devices["workspace"]["source"] != project.WorkspaceVolumeName+"/workspace" {
		t.Fatalf("workspace source = %q", plan.Devices["workspace"]["source"])
	}
}

func TestDefaultAppPortForTemplate(t *testing.T) {
	for _, template := range []string{"", TemplateAI, TemplateBase} {
		port, err := DefaultAppPortForTemplate(template)
		if err != nil {
			t.Fatal(err)
		}
		if port != DefaultAppPort {
			t.Fatalf("port = %d for %q", port, template)
		}
	}
	if _, err := DefaultAppPortForTemplate("unknown"); err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanCreateRejectsUnknownTemplate(t *testing.T) {
	_, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), nil, CreateRequest{
		Reference: "alice/myproject/codex",
		Template:  "unknown",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported sandbox template") {
		t.Fatalf("error = %q", err)
	}
}

func TestPlanCreateRejectsReservedName(t *testing.T) {
	_, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), project.MemoryStore{}, nil, CreateRequest{Reference: "alice/myproject/dns"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanCreateIssuesCertificateFilesWhenProjectCAIsProvided(t *testing.T) {
	ca, err := certs.GenerateCA("test CA", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), nil, CreateRequest{
		Reference:               "alice/myproject/codex",
		ProjectCACertificatePEM: ca.CertificatePEM,
		ProjectCAPrivateKeyPEM:  ca.PrivateKeyPEM,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.CertificateFiles) != 2 {
		t.Fatalf("CertificateFiles length = %d", len(plan.CertificateFiles))
	}
	if plan.CertificateFiles[0].Path != SandboxCertPath {
		t.Fatalf("cert path = %q", plan.CertificateFiles[0].Path)
	}
	if plan.CertificateFiles[1].Path != SandboxCertKeyPath {
		t.Fatalf("key path = %q", plan.CertificateFiles[1].Path)
	}
	if !strings.Contains(string(plan.CertificateFiles[0].Content), "BEGIN CERTIFICATE") {
		t.Fatal("certificate PEM missing")
	}
	if !strings.Contains(string(plan.CertificateFiles[1].Content), "BEGIN EC PRIVATE KEY") {
		t.Fatal("private key PEM missing")
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "BEGIN EC PRIVATE KEY") {
		t.Fatalf("plan JSON leaked private key: %s", encoded)
	}
}

func TestPlanCreateAllocatesNextFreeSandboxIP(t *testing.T) {
	plan, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), fakeSandboxStore{sandboxes: []meta.Sandbox{
		{Name: "codex", PrivateIP: "10.248.0.20"},
		{Name: "claude", PrivateIP: "10.248.0.21"},
	}}, CreateRequest{Reference: "alice/myproject/gemini"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.PrivateIP != "10.248.0.22" {
		t.Fatalf("PrivateIP = %q, want 10.248.0.22", plan.PrivateIP)
	}
}

func TestPlanCreateReusesExistingSandboxIP(t *testing.T) {
	plan, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), fakeSandboxStore{sandboxes: []meta.Sandbox{
		{Name: "codex", PrivateIP: "10.248.0.42"},
	}}, CreateRequest{Reference: "alice/myproject/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.PrivateIP != "10.248.0.42" {
		t.Fatalf("PrivateIP = %q, want 10.248.0.42", plan.PrivateIP)
	}
}

func TestPlanCreateRequiresShareHomeForRunningSandboxHomeReuse(t *testing.T) {
	_, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), fakeSandboxStore{sandboxes: []meta.Sandbox{
		{Name: "codex", HomeDir: "shared", PrivateIP: "10.248.0.20", Running: true},
	}}, CreateRequest{Reference: "alice/myproject/claude", HomeDir: "shared"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--share-home") {
		t.Fatalf("error = %q", err)
	}
}

func TestPlanCreateAllowsExplicitShareHome(t *testing.T) {
	plan, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), fakeSandboxStore{sandboxes: []meta.Sandbox{
		{Name: "codex", HomeDir: "shared", PrivateIP: "10.248.0.20", Running: true},
	}}, CreateRequest{Reference: "alice/myproject/claude", HomeDir: "shared", ShareHome: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.HomeDir != "shared" {
		t.Fatalf("HomeDir = %q", plan.HomeDir)
	}
}

func projectStoreForTest(t *testing.T) project.MemoryStore {
	t.Helper()
	projectConfig, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	return project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-alice-myproject",
		Config: projectConfig,
	}}}
}

type fakeSandboxStore struct {
	sandboxes []meta.Sandbox
}

func (s fakeSandboxStore) ListSandboxes(ctx context.Context, summary project.Summary) ([]meta.Sandbox, error) {
	return s.sandboxes, nil
}
