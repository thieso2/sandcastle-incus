package cli

import (
	"strings"
	"testing"
)

func TestResolveConnectTarget(t *testing.T) {
	// All the enrolled remotes the client "has" for these cases.
	enrolled := map[string]bool{
		"obelix-sc":      true,
		"obelix-web":     true,
		"obelix-default": true, // the install is "known" (logged in)
	}
	exists := func(name string) bool { return enrolled[name] }
	known := func(suffix string) bool { return enrolled[suffix+"-default"] }

	t.Run("no suffix is same-install (no switch)", func(t *testing.T) {
		got, err := resolveConnectTarget("", "sc", "castle", exists, known)
		if err != nil || got != "" {
			t.Fatalf("got %q, %v; want no switch", got, err)
		}
	})

	t.Run("suffix equal to current install is no switch", func(t *testing.T) {
		got, err := resolveConnectTarget("castle", "sc", "castle", exists, known)
		if err != nil || got != "" {
			t.Fatalf("got %q, %v; want no switch", got, err)
		}
	})

	t.Run("cross-install with an enrolled remote returns the target", func(t *testing.T) {
		got, err := resolveConnectTarget("obelix", "sc", "castle", exists, known)
		if err != nil {
			t.Fatal(err)
		}
		if got != "obelix-sc" {
			t.Fatalf("switchTo = %q, want obelix-sc", got)
		}
	})

	t.Run("install known but project not enrolled -> enroll guidance", func(t *testing.T) {
		_, err := resolveConnectTarget("obelix", "db", "castle", exists, known)
		if err == nil {
			t.Fatal("expected an error for a non-enrolled project remote")
		}
		for _, want := range []string{"obelix-db", "sc enroll", "sc project create db"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q missing %q", err.Error(), want)
			}
		}
		if strings.Contains(err.Error(), "sc login") {
			t.Fatalf("known install must NOT suggest sc login: %q", err.Error())
		}
	})

	t.Run("install never touched -> login guidance", func(t *testing.T) {
		_, err := resolveConnectTarget("newbox", "sc", "castle", exists, known)
		if err == nil {
			t.Fatal("expected an error for an unknown install")
		}
		for _, want := range []string{"newbox", "sc login"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q missing %q", err.Error(), want)
			}
		}
		if strings.Contains(err.Error(), "sc enroll") {
			t.Fatalf("unknown install must NOT suggest sc enroll: %q", err.Error())
		}
	})
}
