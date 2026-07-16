package cli

import (
	"slices"
	"testing"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
)

// TestApplyRemoteSwitch verifies that switching the active remote re-points the
// auth hostname, broker, and token to the target install (from the maps recorded
// at login) — the shared logic behind `sc remote switch` and `sc config set remote`.
func TestApplyRemoteSwitch(t *testing.T) {
	base := func() scconfig.SandcastleConfig {
		return scconfig.SandcastleConfig{
			Remote:       "idefix",
			AuthHostname: "https://idefix.thieso2.dev",
			AuthToken:    "idefix-tok",
			Broker:       "https://10.124.0.1:9443",
			Installs: map[string]string{
				"idefix": "https://idefix.thieso2.dev",
				"obelix": "https://obelix.thieso2.dev",
				"julius": "https://home.thieso2.dev",
			},
			Brokers:    map[string]string{"https://obelix.thieso2.dev": "https://10.123.0.1:9443"},
			AuthTokens: map[string]string{"https://obelix.thieso2.dev": "obelix-tok"},
		}
	}

	t.Run("re-points everything for a fully-recorded install", func(t *testing.T) {
		cfg := base()
		fx := applyRemoteSwitch(&cfg, "obelix")
		if cfg.Remote != "obelix" {
			t.Fatalf("Remote = %q, want obelix", cfg.Remote)
		}
		if cfg.AuthHostname != "https://obelix.thieso2.dev" || fx.AuthHostname != "https://obelix.thieso2.dev" {
			t.Fatalf("AuthHostname = %q (fx %q)", cfg.AuthHostname, fx.AuthHostname)
		}
		if cfg.Broker != "https://10.123.0.1:9443" || fx.Broker != "https://10.123.0.1:9443" {
			t.Fatalf("Broker = %q (fx %q)", cfg.Broker, fx.Broker)
		}
		if cfg.AuthToken != "obelix-tok" || !fx.TokenSynced {
			t.Fatalf("AuthToken = %q (synced %v)", cfg.AuthToken, fx.TokenSynced)
		}
	})

	t.Run("clears stale broker+token when nothing recorded for the target", func(t *testing.T) {
		cfg := base()
		fx := applyRemoteSwitch(&cfg, "julius") // in installs, but no broker/token recorded
		if cfg.Remote != "julius" || cfg.AuthHostname != "https://home.thieso2.dev" {
			t.Fatalf("switch to julius: remote=%q host=%q", cfg.Remote, cfg.AuthHostname)
		}
		if cfg.Broker != "" || !fx.BrokerCleared {
			t.Fatalf("stale broker not cleared: %q (cleared %v)", cfg.Broker, fx.BrokerCleared)
		}
		if cfg.AuthToken != "" || !fx.TokenCleared {
			t.Fatalf("stale token not cleared: %q (cleared %v)", cfg.AuthToken, fx.TokenCleared)
		}
	})

	t.Run("unknown remote sets name but leaves auth plane untouched", func(t *testing.T) {
		cfg := base()
		fx := applyRemoteSwitch(&cfg, "not-an-install")
		if cfg.Remote != "not-an-install" {
			t.Fatalf("Remote = %q", cfg.Remote)
		}
		if cfg.AuthHostname != "https://idefix.thieso2.dev" || fx.AuthHostname != "" {
			t.Fatalf("auth hostname changed for unknown remote: %q (fx %q)", cfg.AuthHostname, fx.AuthHostname)
		}
	})
}

// TestSandcastleRemoteRows filters system incus remotes out of `sc remote list`
// and keeps the Sandcastle installs (project-pinned or in the installs map), sorted.
func TestSandcastleRemoteRows(t *testing.T) {
	remotes := []localRemote{
		{Name: "local"},
		{Name: "images"},
		{Name: "obelix", Project: "obelix-thieso2-work"},
		{Name: "idefix", Project: "idefix-thieso2-home"},
		{Name: "julius"}, // no project pin, but recorded in installs
	}
	cfg := scconfig.SandcastleConfig{Installs: map[string]string{
		"julius": "https://home.thieso2.dev",
		"idefix": "https://idefix.thieso2.dev",
	}}
	rows := sandcastleRemoteRows(remotes, cfg)
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.Name)
	}
	if want := []string{"idefix", "julius", "obelix"}; !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v (system remotes excluded, sorted)", got, want)
	}
	if rows[0].AuthHostname != "https://idefix.thieso2.dev" {
		t.Fatalf("idefix auth hostname = %q", rows[0].AuthHostname)
	}
	if !remoteNameKnown(remotes, "obelix") || remoteNameKnown(remotes, "nope") {
		t.Fatalf("remoteNameKnown wrong")
	}
}

// TestRepinProjectForRemote checks the short-project derivation used to re-pin
// the project when switching installs (obelix-thieso2-work -> work).
func TestRepinProjectForRemote(t *testing.T) {
	if got := shortProjectName("obelix-thieso2-work", "thieso2"); got != "work" {
		t.Fatalf("shortProjectName = %q, want work", got)
	}
	if got := shortProjectName("idefix-thieso2-home", "thieso2"); got != "home" {
		t.Fatalf("shortProjectName = %q, want home", got)
	}
	// A pin whose tenant segment doesn't match yields "" (leave the pin untouched).
	if got := shortProjectName("obelix-other-work", "thieso2"); got != "" {
		t.Fatalf("shortProjectName mismatch tenant = %q, want empty", got)
	}
}

// TestSplitRemotePrefix covers `sc ls` addressing: a leading "<remote>:" targets
// another install, the rest is the project.
func TestSplitRemotePrefix(t *testing.T) {
	cases := []struct{ in, remote, rest string }{
		{"obelix:home", "obelix", "home"},
		{"obelix:", "obelix", ""},
		{"home", "", "home"},
		{"", "", ""},
		{" obelix : home ", "obelix", "home"},
	}
	for _, c := range cases {
		remote, rest := splitRemotePrefix(c.in)
		if remote != c.remote || rest != c.rest {
			t.Fatalf("splitRemotePrefix(%q) = (%q,%q), want (%q,%q)", c.in, remote, rest, c.remote, c.rest)
		}
	}
}
