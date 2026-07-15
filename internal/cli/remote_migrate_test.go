package cli

import "testing"

func TestPlanRemoteMigration(t *testing.T) {
	const endpoint = "https://10.61.1.3:8443"
	remotes := []localRemote{
		{Name: "thieso2-io", Endpoint: endpoint, Project: "sc2-thieso2-io"},         // legacy per-project -> rename
		{Name: "sc-majestix-thieso2", Endpoint: endpoint, Project: "sc2-thieso2-default"}, // legacy base -> castle-default
		{Name: "castle-web", Endpoint: endpoint, Project: "sc2-thieso2-web"},        // already migrated -> skip
		{Name: "sc-thieso2-infra", Endpoint: endpoint, Project: "sc2-thieso2"},      // infra project -> skip
		{Name: "thieso2-io-elsewhere", Endpoint: "https://10.99.0.9:8443", Project: "sc2-thieso2-io"}, // different install -> skip
	}
	plan := planRemoteMigration(remotes, "thieso2", "castle", endpoint)

	got := map[string]remoteRename{}
	for _, r := range plan {
		got[r.From] = r
	}
	if len(plan) != 2 {
		t.Fatalf("plan has %d renames, want 2: %+v", len(plan), plan)
	}
	if r, ok := got["thieso2-io"]; !ok || r.To != "castle-io" {
		t.Fatalf("thieso2-io rename wrong: %+v", r)
	}
	if r, ok := got["sc-majestix-thieso2"]; !ok || r.To != "castle-default" {
		t.Fatalf("base remote should become castle-default: %+v", r)
	}
	if _, ok := got["castle-web"]; ok {
		t.Fatal("already-migrated remote must be skipped (idempotent)")
	}
	if _, ok := got["sc-thieso2-infra"]; ok {
		t.Fatal("infra-pinned remote must be skipped")
	}
	if _, ok := got["thieso2-io-elsewhere"]; ok {
		t.Fatal("a different install's remote must never be renamed")
	}
}

func TestPlanRemoteMigration_NoopWhenInputsMissing(t *testing.T) {
	remotes := []localRemote{{Name: "thieso2-io", Endpoint: "e", Project: "sc2-thieso2-io"}}
	if plan := planRemoteMigration(remotes, "thieso2", "", "e"); plan != nil {
		t.Fatalf("empty suffix must produce no plan, got %+v", plan)
	}
	if plan := planRemoteMigration(remotes, "", "castle", "e"); plan != nil {
		t.Fatalf("empty tenant must produce no plan, got %+v", plan)
	}
	// The safety property: an unknown install endpoint must migrate NOTHING —
	// never widen scope to another install's same-named remotes.
	if plan := planRemoteMigration(remotes, "thieso2", "castle", ""); plan != nil {
		t.Fatalf("empty endpoint must produce no plan (fail safe), got %+v", plan)
	}
}
