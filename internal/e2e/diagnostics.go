package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/project"
)

func logProjectDiagnostics(t *testing.T, ctx context.Context, store project.IncusProjectStore, runID string) {
	t.Helper()
	projects, err := project.List(ctx, store)
	if err != nil {
		t.Logf("diagnostics: list projects failed: %v", err)
		return
	}
	var lines []string
	for _, summary := range projects {
		if strings.Contains(summary.Owner, runID) || strings.Contains(summary.Name, runID) || strings.Contains(summary.IncusName, runID) {
			lines = append(lines, fmt.Sprintf(
				"%s/%s incus=%s domain=%s cidr=%s status=%s",
				summary.Owner,
				summary.Name,
				summary.IncusName,
				summary.Domain,
				summary.PrivateCIDR,
				summary.Status,
			))
		}
	}
	if len(lines) == 0 {
		t.Logf("diagnostics: no Sandcastle projects matched run id %q", runID)
		return
	}
	t.Logf("diagnostics: matching Sandcastle projects:\n%s", strings.Join(lines, "\n"))
}
