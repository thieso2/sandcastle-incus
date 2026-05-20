package e2e

import (
	"strings"
	"testing"
)

func TestLoadConfigDefaultsToDisabledAndSandcastleTag(t *testing.T) {
	t.Setenv("SANDCASTLE_E2E", "0")
	t.Setenv("SANDCASTLE_E2E_TAILSCALE_TAG", "")

	config := LoadConfig()
	if config.Enabled {
		t.Fatal("config.Enabled = true, want false")
	}
	if config.Tailscale.Tag != "tag:sandcastle" {
		t.Fatalf("Tailscale tag = %q, want tag:sandcastle", config.Tailscale.Tag)
	}
}

func TestValidateFailsClosedWhenE2EDisabled(t *testing.T) {
	config := LoadConfig()
	err := config.Validate()
	if err == nil {
		t.Fatal("expected disabled e2e error")
	}
	if !strings.Contains(err.Error(), "SANDCASTLE_E2E=1") {
		t.Fatalf("error = %q, want SANDCASTLE_E2E hint", err.Error())
	}
}

func TestValidateAcceptsMinimalEnabledConfig(t *testing.T) {
	t.Setenv("SANDCASTLE_E2E", "1")

	config := LoadConfig()
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
}
