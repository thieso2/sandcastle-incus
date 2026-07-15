package cli

import (
	"strings"
	"testing"
)

func TestResolveConnectTarget(t *testing.T) {
	// All the enrolled remotes the client "has" for these cases.
	enrolled := map[string]bool{
		"obelix-sc":  true,
		"obelix-web": true,
	}
	exists := func(name string) bool { return enrolled[name] }

	t.Run("no suffix is same-install (no switch)", func(t *testing.T) {
		got, err := resolveConnectTarget("", "sc", "castle", exists)
		if err != nil || got != "" {
			t.Fatalf("got %q, %v; want no switch", got, err)
		}
	})

	t.Run("suffix equal to current install is no switch", func(t *testing.T) {
		got, err := resolveConnectTarget("castle", "sc", "castle", exists)
		if err != nil || got != "" {
			t.Fatalf("got %q, %v; want no switch", got, err)
		}
	})

	t.Run("cross-install with an enrolled remote returns the target", func(t *testing.T) {
		got, err := resolveConnectTarget("obelix", "sc", "castle", exists)
		if err != nil {
			t.Fatal(err)
		}
		if got != "obelix-sc" {
			t.Fatalf("switchTo = %q, want obelix-sc", got)
		}
	})

	t.Run("cross-install with a missing remote errors with enroll guidance", func(t *testing.T) {
		_, err := resolveConnectTarget("obelix", "db", "castle", exists)
		if err == nil {
			t.Fatal("expected an error for a non-enrolled target remote")
		}
		for _, want := range []string{"obelix-db", "obelix", "sc login"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q missing %q", err.Error(), want)
			}
		}
	})
}
