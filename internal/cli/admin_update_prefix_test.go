package cli

import (
	"reflect"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/meta"
)

func TestDiscoverInstallPrefixes(t *testing.T) {
	tests := []struct {
		name       string
		components []incusx.ComponentVersion
		want       []string
	}{
		{
			name:       "empty fleet",
			components: nil,
			want:       []string{},
		},
		{
			name: "single install from auth-app",
			components: []incusx.ComponentVersion{
				{Kind: meta.KindAuthApp, Project: "idefix-infra"},
				{Kind: meta.KindSidecar, Project: "idefix-thieso2", Tenant: "thieso2"},
			},
			want: []string{"idefix"},
		},
		{
			name: "two installs, sorted, deduped across auth-app+broker",
			components: []incusx.ComponentVersion{
				{Kind: meta.KindBroker, Project: "sc2-broker"},
				{Kind: meta.KindAuthApp, Project: "sc2-infra"},
				{Kind: meta.KindAuthApp, Project: "idefix-infra"},
			},
			want: []string{"idefix", "sc2"},
		},
		{
			name: "legacy unprefixed appliances are skipped",
			components: []incusx.ComponentVersion{
				{Kind: meta.KindAuthApp, Project: incusx.AuthAppDefaultProject},
			},
			want: []string{},
		},
		{
			name: "sidecars alone do not define an install",
			components: []incusx.ComponentVersion{
				{Kind: meta.KindSidecar, Project: "idefix-thieso2", Tenant: "thieso2"},
			},
			want: []string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := discoverInstallPrefixes(tc.components)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("discoverInstallPrefixes = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveUpdatePrefix(t *testing.T) {
	// The flag always wins and is normalized to the v2 prefix ("sc" → "sc2",
	// others pass through).
	t.Run("flag wins and normalizes", func(t *testing.T) {
		cfg := commandConfig{stderr: writerDiscard{}}
		got, err := resolveUpdatePrefix("idefix", cfg, nil)
		if err != nil || got != "idefix" {
			t.Fatalf("got %q, %v; want idefix", got, err)
		}
		got, err = resolveUpdatePrefix("sc", cfg, nil)
		if err != nil || got != "sc2" {
			t.Fatalf("got %q, %v; want sc2", got, err)
		}
	})

	// No flag, no env, exactly one install ⇒ auto-detect it.
	t.Run("auto-detect single install", func(t *testing.T) {
		t.Setenv("SANDCASTLE_INCUS_PROJECT_PREFIX", "")
		t.Setenv("SANDCASTLE_PROJECT_PREFIX", "")
		cfg := commandConfig{stderr: writerDiscard{}}
		components := []incusx.ComponentVersion{{Kind: meta.KindAuthApp, Project: "idefix-infra"}}
		got, err := resolveUpdatePrefix("", cfg, components)
		if err != nil || got != "idefix" {
			t.Fatalf("got %q, %v; want idefix", got, err)
		}
	})

	// No flag, no env, several installs ⇒ refuse and require --prefix.
	t.Run("multiple installs error", func(t *testing.T) {
		t.Setenv("SANDCASTLE_INCUS_PROJECT_PREFIX", "")
		t.Setenv("SANDCASTLE_PROJECT_PREFIX", "")
		cfg := commandConfig{stderr: writerDiscard{}}
		components := []incusx.ComponentVersion{
			{Kind: meta.KindAuthApp, Project: "idefix-infra"},
			{Kind: meta.KindAuthApp, Project: "sc2-infra"},
		}
		if _, err := resolveUpdatePrefix("", cfg, components); err == nil {
			t.Fatal("expected an error naming --prefix, got nil")
		}
	})
}

// writerDiscard is a no-op io.Writer for exercising code that logs to stderr.
type writerDiscard struct{}

func (writerDiscard) Write(p []byte) (int, error) { return len(p), nil }
