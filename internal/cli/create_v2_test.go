package cli

import (
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

func TestParseV2MachineReference(t *testing.T) {
	tests := []struct {
		name           string
		reference      string
		currentProject string
		wantProject    string
		wantMachine    string
		wantErr        bool
	}{
		{"bare machine", "dev", "", "default", "dev", false},
		{"machine with current project", "dev", "backend", "backend", "dev", false},
		{"explicit project", "web:dev", "backend", "web", "dev", false},
		{"tenant qualified", "acme/web:dev", "", "web", "dev", false},
		{"wrong tenant", "other/dev", "", "", "", true},
		{"empty machine", "web:", "", "", "", true},
		{"invalid machine name", "Bad Name", "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project, machine, err := parseV2MachineReference(tt.reference, "acme", tt.currentProject)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got project=%q machine=%q", project, machine)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if project != tt.wantProject || machine != tt.wantMachine {
				t.Fatalf("got %q/%q, want %q/%q", project, machine, tt.wantProject, tt.wantMachine)
			}
		})
	}
}

func TestResolveV2MachineReference(t *testing.T) {
	summary := tenant.Summary{
		Tenant:   "acme",
		Projects: []meta.Project{{Name: "default"}, {Name: "test2"}, {Name: "test3"}},
	}

	t.Run("known project passes", func(t *testing.T) {
		project, machine, err := resolveV2MachineReference(summary, "test2:dev", "")
		if err != nil {
			t.Fatal(err)
		}
		if project != "test2" || machine != "dev" {
			t.Fatalf("got %q/%q", project, machine)
		}
	})

	t.Run("unknown project fails with the project list", func(t *testing.T) {
		_, _, err := resolveV2MachineReference(summary, "nosuch:dev", "")
		if err == nil {
			t.Fatal("expected error")
		}
		for _, want := range []string{`project "nosuch" not found`, "default, test2, test3", "sc project create nosuch"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q missing %q", err.Error(), want)
			}
		}
	})

	t.Run("swapped reference suggests project:machine", func(t *testing.T) {
		// the user's real mistake: `sc delete dev:test2` for machine dev in project test2
		_, _, err := resolveV2MachineReference(summary, "dev:test2", "")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), `did you mean "test2:dev"`) {
			t.Fatalf("error %q missing the swap hint", err.Error())
		}
	})
}
