package project

import (
	"context"
	"fmt"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
)

type Status struct {
	Summary Summary `json:"summary"`
	Checks  []Check `json:"checks"`
}

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type TopologyStore interface {
	GetTopology(ctx context.Context, request TopologyRequest) (Topology, error)
}

type TopologyRequest struct {
	IncusProject string
	StoragePool  string
	Domain       string
}

type Topology struct {
	PrivateNetworkPresent bool
	DurableVolumes        map[string]bool
	Sidecars              map[string]SidecarStatus
	DiagnosticFiles       []DiagnosticFile
}

type SidecarStatus struct {
	Present bool
	Running bool
	Status  string
}

type DiagnosticFile struct {
	Instance string `json:"instance"`
	Path     string `json:"path"`
	Content  string `json:"content,omitempty"`
	Error    string `json:"error,omitempty"`
}

func GetStatus(ctx context.Context, store IncusProjectStore, reference string) (Status, error) {
	return GetStatusWithTopology(ctx, store, nil, TopologyRequest{}, reference)
}

func GetStatusWithTopology(ctx context.Context, store IncusProjectStore, topologyStore TopologyStore, topologyRequest TopologyRequest, reference string) (Status, error) {
	ref, err := naming.ParseProjectRef(reference)
	if err != nil {
		return Status{}, err
	}
	projects, err := List(ctx, store)
	if err != nil {
		return Status{}, err
	}
	for _, summary := range projects {
		if summary.Owner == ref.Owner && summary.Name == ref.Project {
			status := Status{
				Summary: summary,
				Checks: []Check{
					{Name: "metadata", Status: "ok", Detail: "Sandcastle project metadata is present"},
					{Name: "cidr", Status: checkPresent(summary.PrivateCIDR), Detail: summary.PrivateCIDR},
					{Name: "domain", Status: checkPresent(summary.Domain), Detail: summary.Domain},
					tailscaleRouteCheck(summary),
				},
			}
			if topologyStore != nil {
				topologyRequest.IncusProject = summary.IncusName
				topologyRequest.Domain = summary.Domain
				topology, err := topologyStore.GetTopology(ctx, topologyRequest)
				if err != nil {
					status.Checks = append(status.Checks, Check{Name: "topology", Status: "error", Detail: err.Error()})
					return status, nil
				}
				status.Checks = append(status.Checks, TopologyChecks(topology)...)
			}
			return status, nil
		}
	}
	return Status{}, fmt.Errorf("Sandcastle project %s not found", ref.String())
}

func tailscaleRouteCheck(summary Summary) Check {
	if summary.Tailscale.State == "" {
		return Check{Name: "tailscale:route", Status: "unknown", Detail: "no Tailscale status recorded"}
	}
	if summary.Tailscale.State == meta.TailscaleStateRunningLoggedOut {
		return Check{Name: "tailscale:route", Status: "unknown", Detail: "Tailscale sidecar is running but not authenticated"}
	}
	for _, route := range summary.Tailscale.AdvertisedRoutes {
		if route == summary.PrivateCIDR {
			return Check{Name: "tailscale:route", Status: "ok", Detail: route}
		}
	}
	return Check{Name: "tailscale:route", Status: "missing", Detail: summary.PrivateCIDR}
}

func checkPresent(value string) string {
	if value == "" {
		return "missing"
	}
	return "ok"
}

// TopologyChecks returns stable project resource checks for status and diagnostics output.
func TopologyChecks(topology Topology) []Check {
	checks := []Check{
		{Name: "network:sc-private", Status: presentStatus(topology.PrivateNetworkPresent)},
	}
	for _, volume := range []string{HomeVolumeName, WorkspaceVolumeName, CAVolumeName} {
		checks = append(checks, Check{Name: "volume:" + volume, Status: presentStatus(topology.DurableVolumes[volume])})
	}
	for _, sidecar := range []string{TailscaleName, DNSName} {
		status := topology.Sidecars[sidecar]
		check := Check{Name: "sidecar:" + sidecar, Status: presentStatus(status.Present), Detail: status.Status}
		if status.Present && !status.Running {
			check.Status = "stopped"
		}
		if status.Present && status.Running {
			check.Status = "ok"
		}
		checks = append(checks, check)
	}
	return checks
}

func presentStatus(present bool) string {
	if present {
		return "ok"
	}
	return "missing"
}
