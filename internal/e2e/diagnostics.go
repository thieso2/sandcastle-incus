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

func registerProjectDiagnostics(t *testing.T, ctx context.Context, store project.IncusProjectStore, topologyStore project.TopologyStore, storagePool string, runID string) {
	t.Helper()
	t.Cleanup(func() {
		if t.Failed() {
			logProjectDiagnosticsWithTopology(t, ctx, store, topologyStore, storagePool, runID)
		}
	})
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
		if tailscaleLine := projectTailscaleDiagnostics(summary); tailscaleLine != "" {
			line += "\n  tailscale: " + tailscaleLine
		}
		lines = append(lines, line)
	}
	return lines
}

func matchesProjectRun(summary project.Summary, runID string) bool {
	if strings.TrimSpace(runID) == "" {
		return false
	}
	return strings.Contains(summary.Owner, runID) ||
		strings.Contains(summary.Name, runID) ||
		strings.Contains(summary.IncusName, runID) ||
		strings.Contains(summary.Domain, runID)
}

func projectTopologyDiagnostics(ctx context.Context, topologyStore project.TopologyStore, storagePool string, incusProject string) string {
	topology, err := topologyStore.GetTopology(ctx, project.TopologyRequest{IncusProject: incusProject, StoragePool: storagePool})
	if err != nil {
		return "error=" + err.Error()
	}
	var parts []string
	for _, check := range project.TopologyChecks(topology) {
		value := check.Status
		if check.Detail != "" {
			value += "(" + check.Detail + ")"
		}
		parts = append(parts, check.Name+"="+value)
	}
	return strings.Join(parts, " ")
}

func projectTailscaleDiagnostics(summary project.Summary) string {
	state := strings.TrimSpace(summary.Tailscale.State)
	tailnet := strings.TrimSpace(summary.Tailscale.Tailnet)
	hostname := strings.TrimSpace(summary.Tailscale.Hostname)
	routes := summary.Tailscale.AdvertisedRoutes
	ips := summary.Tailscale.TailscaleIPs
	checkedAt := strings.TrimSpace(summary.Tailscale.LastCheckedAt)
	if state == "" && tailnet == "" && hostname == "" && len(routes) == 0 && len(ips) == 0 && checkedAt == "" {
		return ""
	}
	parts := []string{}
	if state != "" {
		parts = append(parts, "state="+redactDiagnosticValue(state))
	}
	if tailnet != "" {
		parts = append(parts, "tailnet="+redactDiagnosticValue(tailnet))
	}
	if hostname != "" {
		parts = append(parts, "hostname="+redactDiagnosticValue(hostname))
	}
	if len(routes) > 0 {
		parts = append(parts, "routes="+redactDiagnosticValue(strings.Join(routes, ",")))
	}
	if len(ips) > 0 {
		parts = append(parts, fmt.Sprintf("ips=%d", len(ips)))
	}
	if checkedAt != "" {
		parts = append(parts, "lastCheckedAt="+redactDiagnosticValue(checkedAt))
	}
	return strings.Join(parts, " ")
}

func redactDiagnosticValue(value string) string {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "login.tailscale.com") ||
		strings.Contains(lower, "tskey-") ||
		strings.Contains(lower, "authkey") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "secret") {
		return "<redacted>"
	}
	return value
}
