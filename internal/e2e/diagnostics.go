package e2e

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/localdns"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
	"gopkg.in/yaml.v2"
)

func logProjectDiagnostics(t *testing.T, ctx context.Context, store project.IncusProjectStore, runID string) {
	logProjectDiagnosticsWithTopology(t, ctx, store, nil, runID)
}

func registerProjectDiagnostics(t *testing.T, ctx context.Context, store project.IncusProjectStore, topologyStore project.TopologyStore, runID string) {
	t.Helper()
	t.Cleanup(func() {
		if t.Failed() {
			logProjectDiagnosticsWithTopology(t, ctx, store, topologyStore, runID)
		}
	})
}

func logProjectDiagnosticsWithTopology(t *testing.T, ctx context.Context, store project.IncusProjectStore, topologyStore project.TopologyStore, runID string) {
	t.Helper()
	projects, err := project.List(ctx, store)
	if err != nil {
		t.Logf("diagnostics: list tenants failed: %v", err)
		return
	}
	lines := projectDiagnosticLines(ctx, projects, topologyStore, runID)
	localDNSLines, err := localDNSDiagnosticLines(localdns.DefaultStatePath(), runID)
	if err != nil {
		t.Logf("diagnostics: local DNS state failed: %v", err)
	}
	lines = append(lines, localDNSLines...)
	if len(lines) == 0 {
		t.Logf("diagnostics: no Sandcastle tenants matched run id %q", runID)
		return
	}
	t.Logf("diagnostics: matching Sandcastle tenants:\n%s", strings.Join(lines, "\n"))
}

func projectDiagnosticLines(ctx context.Context, projects []project.Summary, topologyStore project.TopologyStore, runID string) []string {
	var lines []string
	for _, summary := range projects {
		if !matchesProjectRun(summary, runID) {
			continue
		}
		line := fmt.Sprintf(
			"%s incus=%s dnsSuffix=%s cidr=%s status=%s",
			summary.Tenant,
			summary.IncusName,
			summary.DNSSuffix,
			summary.PrivateCIDR,
			summary.Status,
		)
		if topologyStore != nil {
			line += "\n  topology: " + projectTopologyDiagnostics(ctx, topologyStore, summary)
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
	return strings.Contains(summary.Tenant, runID) ||
		strings.Contains(summary.IncusName, runID) ||
		strings.Contains(summary.DNSSuffix, runID)
}

func projectTopologyDiagnostics(ctx context.Context, topologyStore project.TopologyStore, summary project.Summary) string {
	topology, err := topologyStore.GetTopology(ctx, project.TopologyRequest{
		IncusProject: summary.IncusName,
		DNSSuffix:    summary.DNSSuffix,
	})
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
	output := strings.Join(parts, " ")
	if files := topologyDiagnosticFiles(topology.DiagnosticFiles); files != "" {
		output += "\n  files:\n" + files
	}
	return output
}

func topologyDiagnosticFiles(files []project.DiagnosticFile) string {
	var parts []string
	for _, file := range files {
		label := file.Instance + ":" + file.Path
		if file.Error != "" {
			parts = append(parts, "    "+label+" error="+redactDiagnosticValue(file.Error))
			continue
		}
		content := strings.TrimSpace(file.Content)
		if content == "" {
			parts = append(parts, "    "+label+" empty")
			continue
		}
		parts = append(parts, "    "+label+"\n"+indentDiagnosticContent(redactDiagnosticValue(content), "      "))
	}
	return strings.Join(parts, "\n")
}

func indentDiagnosticContent(content string, prefix string) string {
	lines := strings.Split(content, "\n")
	for index := range lines {
		lines[index] = prefix + lines[index]
	}
	return strings.Join(lines, "\n")
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

func localDNSDiagnosticLines(statePath string, runID string) ([]string, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, nil
	}
	content, err := os.ReadFile(statePath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(content) == 0 {
		return nil, nil
	}
	var state localdns.State
	if err := yaml.Unmarshal(content, &state); err != nil {
		return nil, err
	}
	var lines []string
	for _, entry := range state.Tenants {
		if !matchesLocalDNSRun(entry, runID) {
			continue
		}
		endpoint := "invalid"
		if entry.DNSEndpoint.IP != "" && entry.DNSEndpoint.Port > 0 {
			endpoint = net.JoinHostPort(entry.DNSEndpoint.IP, fmt.Sprint(entry.DNSEndpoint.Port))
		}
		lines = append(lines, fmt.Sprintf(
			"local-dns: %s dnsSuffix=%s endpoint=%s resolver=%s",
			entry.Tenant,
			entry.DNSSuffix,
			endpoint,
			entry.Resolver.Listen,
		))
	}
	return lines, nil
}

func matchesLocalDNSRun(entry localdns.TenantState, runID string) bool {
	return strings.Contains(entry.Tenant, runID) ||
		strings.Contains(entry.DNSSuffix, runID)
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
