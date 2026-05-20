package project

import (
	"context"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
)

func TestListManagedProjects(t *testing.T) {
	aliceConfig, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "zeta",
		Domain:          "zeta.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	bobConfig, err := meta.ProjectConfig(meta.Project{
		Owner:           "bob",
		Project:         "alpha",
		Domain:          "alpha.project-tld",
		PrivateCIDR:     "10.248.1.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	store := MemoryStore{Projects: []IncusProject{
		{Name: "default", Config: map[string]string{}},
		{Name: "sc-bob-alpha", Config: bobConfig},
		{Name: "sc-alice-zeta", Config: aliceConfig},
	}}

	summaries, err := List(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("len(summaries) = %d, want 2", len(summaries))
	}
	if summaries[0].Owner != "alice" || summaries[0].Name != "zeta" {
		t.Fatalf("first summary = %#v", summaries[0])
	}
	if summaries[1].Owner != "bob" || summaries[1].Name != "alpha" {
		t.Fatalf("second summary = %#v", summaries[1])
	}
}

func TestListReportsInvalidManagedMetadata(t *testing.T) {
	store := MemoryStore{Projects: []IncusProject{{
		Name: "sc-alice-broken",
		Config: map[string]string{
			meta.KeyKind:    meta.KindProject,
			meta.KeyVersion: "1",
		},
	}}}

	_, err := List(context.Background(), store)
	if err == nil {
		t.Fatal("expected error")
	}
}
