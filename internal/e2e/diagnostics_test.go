package e2e

import (
	"context"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

func TestLogProjectDiagnosticsDoesNotFailWithoutMatches(t *testing.T) {
	logProjectDiagnostics(t, context.Background(), project.MemoryStore{}, "missing")
}

func TestLogProjectDiagnosticsWithMatch(t *testing.T) {
	config, err := meta.ProjectConfig(meta.Project{
		Owner:           "owner-e2e-test",
		Project:         "project-e2e-test",
		Domain:          "project.e2e.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	store := project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-owner-e2e-test-project-e2e-test",
		Config: config,
	}}}
	logProjectDiagnostics(t, context.Background(), store, "e2e-test")
}
