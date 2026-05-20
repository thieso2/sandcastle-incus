package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/project"
)

func logProjectDiagnostics(t *testing.T, ctx context.Context, store project.IncusProjectStore, runID string) {
	logProjectDiagnosticsWithTopology(t, ctx, store, nil, "", runID)
}

func logProjectDiagnosticsWithTopology(t *testing.T, ctx context.Context, store project.IncusProjectStore, topologyStore project.TopologyStore, storagePool string, runID string) {
	t.Helper()
	projects, err := project.List(ctx, store)
	if err != nil {
		t.Logf("diagnostics: list projects failed: %v", err)
		return
	}
	lines := projectDiagnosticLines(ctx, projects, topologyStore, storagePool, runID)
	if len(lines) == 0 {
		t.Logf("diagnostics: no Sandcastle projects matched run id %q", runID)
		return
	}
	t.Logf("diagnostics: matching Sandcastle projects:\n%s", strings.Join(lines, "\n"))
}

func projectDiagnosticLines(ctx context.Context, projects []project.Summary, topologyStore project.TopologyStore, storagePool string, runID string) []string {
	var lines []string
	for _, summary := range projects {
		if !matchesProjectRun(summary, runID) {
			continue
		}
		line := fmt.Sprintf(
			"%s/%s incus=%s domain=%s cidr=%s status=%s",
			summary.Owner,
			summary.Name,
			summary.IncusName,
			summary.Domain,
			summary.PrivateCIDR,
			summary.Status,
		)
		if topologyStore != nil {
			line += "\n  topology: " + projectTopologyDiagnostics(ctx, topologyStore, storagePool, summary.IncusName)
		}
		lines = append(lines, line)
	}
	return lines
}

func matchesProjectRun(summary project.Summary, runID string) bool {
	return strings.Contains(summary.Owner, runID) || strings.Contains(summary.Name, runID) || strings.Contains(summary.IncusName, runID)
}

func projectTopologyDiagnostics(ctx context.Context, topologyStore project.TopologyStore, storagePool string, incusProject string) string {
	topology, err := topologyStore.GetTopology(ctx, project.TopologyRequest{IncusProject: incusProject, StoragePool: storagePool})
	if err != nil {
		return "error=" + err.Error()
	}
	var parts []string
	for _, check := range projectTopologyDiagnosticChecks(topology) {
		value := check.Status
		if check.Detail != "" {
			value += "(" + check.Detail + ")"
		}
		parts = append(parts, check.Name+"="+value)
	}
	return strings.Join(parts, " ")
}

func projectTopologyDiagnosticChecks(topology project.Topology) []project.Check {
	status := func(present bool) string {
		if present {
			return "ok"
		}
		return "missing"
	}
	checks := []project.Check{
		{Name: "network:" + project.PrivateNetworkName, Status: status(topology.PrivateNetworkPresent)},
	}
	for _, volume := range []string{project.HomeVolumeName, project.WorkspaceVolumeName, project.CAVolumeName} {
		checks = append(checks, project.Check{Name: "volume:" + volume, Status: status(topology.DurableVolumes[volume])})
	}
	for _, sidecar := range []string{project.TailscaleName, project.DNSName} {
		sidecarStatus := topology.Sidecars[sidecar]
		check := project.Check{Name: "sidecar:" + sidecar, Status: status(sidecarStatus.Present), Detail: sidecarStatus.Status}
		if sidecarStatus.Present && !sidecarStatus.Running {
			check.Status = "stopped"
		}
		if sidecarStatus.Present && sidecarStatus.Running {
			check.Status = "ok"
		}
		checks = append(checks, check)
	}
	return checks
}
