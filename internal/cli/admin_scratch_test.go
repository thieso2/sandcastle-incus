package cli

import (
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/incusx"
)

func TestFormatScratchMigration(t *testing.T) {
	got := formatScratchMigration([]incusx.ScratchMigrationStatus{
		{IncusProject: "sc2-acme-default", Machine: "web", Type: "container", Volume: "web-scratch", State: incusx.ScratchAttachedLive},
		{IncusProject: "sc2-acme-default", Machine: "vm1", Type: "virtual-machine", Volume: "vm1-scratch", State: incusx.ScratchAttachedNextRestart},
		{IncusProject: "sc2-acme-default", Machine: "bad", Type: "container", Volume: "bad-scratch", State: incusx.ScratchFailed, Error: "foreign volume"},
	})
	want := "sc2-acme-default/web (container): attached (live) [web-scratch]\n" +
		"sc2-acme-default/vm1 (virtual-machine): attached (VM: restart to mount) [vm1-scratch]\n" +
		"sc2-acme-default/bad (container): FAILED [bad-scratch]: foreign volume"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
