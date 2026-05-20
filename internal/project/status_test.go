package project

import (
	"context"
	"testing"

	"github.com/thieso2/sandcastle-incus/internal/meta"
)

func TestGetStatus(t *testing.T) {
	config, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := GetStatus(context.Background(), MemoryStore{Projects: []IncusProject{{
		Name:   "sc-alice-myproject",
		Config: config,
	}}}, "alice/myproject")
	if err != nil {
		t.Fatal(err)
	}
	if status.Summary.IncusName != "sc-alice-myproject" {
		t.Fatalf("IncusName = %q", status.Summary.IncusName)
	}
	if len(status.Checks) != 3 {
		t.Fatalf("checks = %d, want 3", len(status.Checks))
	}
}

func TestGetStatusWithTopology(t *testing.T) {
	config, err := meta.ProjectConfig(meta.Project{
		Owner:           "alice",
		Project:         "myproject",
		Domain:          "myproject.project-tld",
		PrivateCIDR:     "10.248.0.0/24",
		DefaultTemplate: "ai",
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := GetStatusWithTopology(
		context.Background(),
		MemoryStore{Projects: []IncusProject{{Name: "sc-alice-myproject", Config: config}}},
		fakeTopologyStore{topology: Topology{
			PrivateNetworkPresent: true,
			DurableVolumes: map[string]bool{
				HomeVolumeName:      true,
				WorkspaceVolumeName: true,
				CAVolumeName:        true,
			},
			Sidecars: map[string]SidecarStatus{
				TailscaleName: {Present: true, Running: false, Status: "Stopped"},
				DNSName:       {Present: true, Running: true, Status: "Running"},
			},
		}},
		TopologyRequest{StoragePool: "default"},
		"alice/myproject",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Checks) != 9 {
		t.Fatalf("checks = %d, want 9", len(status.Checks))
	}
	if status.Checks[7].Status != "stopped" {
		t.Fatalf("tailscale check = %#v", status.Checks[7])
	}
	if status.Checks[8].Status != "ok" {
		t.Fatalf("dns check = %#v", status.Checks[8])
	}
}

func TestGetStatusReportsMissingProject(t *testing.T) {
	_, err := GetStatus(context.Background(), MemoryStore{}, "alice/missing")
	if err == nil {
		t.Fatal("expected error")
	}
}

type fakeTopologyStore struct {
	topology Topology
}

func (s fakeTopologyStore) GetTopology(ctx context.Context, request TopologyRequest) (Topology, error) {
	return s.topology, nil
}
