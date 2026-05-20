package e2e

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

func TestLogProjectDiagnosticsDoesNotFailWithoutMatches(t *testing.T) {
	logProjectDiagnostics(t, context.Background(), project.MemoryStore{}, "missing")
}

func TestLogProjectDiagnosticsWithMatch(t *testing.T) {
	store := diagnosticProjectStore(t)
	logProjectDiagnostics(t, context.Background(), store, "e2e-test")
}

func TestProjectDiagnosticLinesIncludeTopology(t *testing.T) {
	store := diagnosticProjectStore(t)
	summaries, err := project.List(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	lines := projectDiagnosticLines(context.Background(), summaries, fakeDiagnosticTopologyStore{
		topology: project.Topology{
			PrivateNetworkPresent: true,
			DurableVolumes: map[string]bool{
				project.HomeVolumeName: true,
				project.CAVolumeName:   true,
			},
			Sidecars: map[string]project.SidecarStatus{
				project.TailscaleName: {Present: true, Running: true, Status: "Running"},
				project.DNSName:       {Present: true, Running: false, Status: "Stopped"},
			},
		},
	}, "default", "e2e-test")
	if len(lines) != 1 {
		t.Fatalf("lines = %#v, want one diagnostic line", lines)
	}
	for _, want := range []string{
		"topology:",
		"network:sc-private=ok",
		"volume:sc-home=ok",
		"volume:sc-workspace=missing",
		"sidecar:sc-tailscale=ok(Running)",
		"sidecar:sc-dns=stopped(Stopped)",
	} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("diagnostic line missing %q:\n%s", want, lines[0])
		}
	}
}

func TestProjectDiagnosticLinesIncludeTopologyErrors(t *testing.T) {
	store := diagnosticProjectStore(t)
	summaries, err := project.List(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	lines := projectDiagnosticLines(context.Background(), summaries, fakeDiagnosticTopologyStore{err: errors.New("boom")}, "default", "e2e-test")
	if len(lines) != 1 {
		t.Fatalf("lines = %#v, want one diagnostic line", lines)
	}
	if !strings.Contains(lines[0], "topology: error=boom") {
		t.Fatalf("diagnostic line missing topology error:\n%s", lines[0])
	}
}

func TestProjectDiagnosticLinesMatchRunIDInDomain(t *testing.T) {
	config, err := meta.ProjectConfig(meta.Project{
		Owner:           "owner",
		Project:         "project",
		Domain:          "project.e2e-domain-only.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := project.List(context.Background(), project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-owner-project",
		Config: config,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	lines := projectDiagnosticLines(context.Background(), summaries, nil, "", "e2e-domain-only")
	if len(lines) != 1 {
		t.Fatalf("lines = %#v, want one diagnostic line", lines)
	}
	if !strings.Contains(lines[0], "project.e2e-domain-only.project-tld") {
		t.Fatalf("diagnostic line missing domain:\n%s", lines[0])
	}
}

func diagnosticProjectStore(t *testing.T) project.MemoryStore {
	t.Helper()
	config, err := meta.ProjectConfig(meta.Project{
		Owner:           "owner-e2e-test",
		Project:         "project-e2e-test",
		Domain:          "project.e2e.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	return project.MemoryStore{Projects: []project.IncusProject{{
		Name:   "sc-owner-e2e-test-project-e2e-test",
		Config: config,
	}}}
}

type fakeDiagnosticTopologyStore struct {
	topology project.Topology
	err      error
}

func (s fakeDiagnosticTopologyStore) GetTopology(ctx context.Context, request project.TopologyRequest) (project.Topology, error) {
	if s.err != nil {
		return project.Topology{}, s.err
	}
	return s.topology, nil
}
