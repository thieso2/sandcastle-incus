package project

import (
	"context"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
)

func TestGetStatus(t *testing.T) {
	config, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := GetStatus(context.Background(), MemoryStore{Projects: []IncusProject{{
		Name:   "sc-alice-myproject",
		Config: config,
	}}}, "alice/myproject")
	if err != nil {
		t.Fatal(err)
	}
	if status.Summary.IncusName != "sc-alice-myproject" {
		t.Fatalf("IncusName = %q", status.Summary.IncusName)
	}
	if len(status.Checks) != 3 {
		t.Fatalf("checks = %d, want 3", len(status.Checks))
	}
}

func TestGetStatusReportsMissingProject(t *testing.T) {
	_, err := GetStatus(context.Background(), MemoryStore{}, "alice/missing")
	if err == nil {
		t.Fatal("expected error")
	}
}
