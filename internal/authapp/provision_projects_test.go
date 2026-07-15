package authapp

import (
	"slices"
	"testing"
)

// TestShortProjectNames locks in the fix for the login pinning the full Incus
// project name (e.g. "obelix-thieso2-work") into config.Project instead of the
// short name ("work"), which then failed to resolve against the tenant's short
// project list.
func TestShortProjectNames(t *testing.T) {
	cases := []struct {
		name         string
		fullNames    []string
		defaultFull  string
		defaultShort string
		want         []string
	}{
		{
			name:         "single project strips prefix",
			fullNames:    []string{"obelix-thieso2-work"},
			defaultFull:  "obelix-thieso2-work",
			defaultShort: "work",
			want:         []string{"work"},
		},
		{
			name:         "multiple projects share the prefix",
			fullNames:    []string{"sc2-acme-default", "sc2-acme-web"},
			defaultFull:  "sc2-acme-default",
			defaultShort: "default",
			want:         []string{"default", "web"},
		},
		{
			name:         "missing short pair passes through unchanged",
			fullNames:    []string{"sc2-acme-default"},
			defaultFull:  "sc2-acme-default",
			defaultShort: "",
			want:         []string{"sc2-acme-default"},
		},
		{
			name:         "names without the prefix pass through",
			fullNames:    []string{"unrelated"},
			defaultFull:  "obelix-thieso2-work",
			defaultShort: "work",
			want:         []string{"unrelated"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shortProjectNames(tc.fullNames, tc.defaultFull, tc.defaultShort)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("shortProjectNames(%v, %q, %q) = %v, want %v",
					tc.fullNames, tc.defaultFull, tc.defaultShort, got, tc.want)
			}
		})
	}
}
