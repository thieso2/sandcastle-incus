package cli

import "testing"

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
