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
	}}}, CreateRequest{Reference: "alice/myproject/codex"})
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
	if plan.CAVolume != project.CAVolumeName {
		t.Fatalf("CAVolume = %q", plan.CAVolume)
	}
	if plan.MetadataConfig[meta.KeyKind] != meta.KindSandbox {
		t.Fatalf("metadata kind = %q", plan.MetadataConfig[meta.KeyKind])
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
}

func TestPlanCreateRejectsReservedName(t *testing.T) {
	_, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), project.MemoryStore{}, CreateRequest{Reference: "alice/myproject/dns"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPlanCreateIssuesCertificateFilesWhenProjectCAIsProvided(t *testing.T) {
	ca, err := certs.GenerateCA("test CA", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanCreate(context.Background(), config.LoadAdminFromEnv(), projectStoreForTest(t), CreateRequest{
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
