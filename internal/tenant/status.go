package tenant

import (
	"context"
	"fmt"

	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
)

type Status struct {
	Summary Summary     `json:"summary"`
	Checks  []Check     `json:"checks"`
	Shares  ShareHealth `json:"shares"`
}

type ShareHealth struct {
	OutboundShareCount       int `json:"outboundShareCount"`
	InboundAcceptedCount     int `json:"inboundAcceptedCount"`
	PendingInboundOfferCount int `json:"pendingInboundOfferCount"`
	UnreconciledMachineCount int `json:"unreconciledMachineCount"`
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
	InfraProject string
	DNSSuffix    string
}

type Topology struct {
	PrivateNetworkPresent bool
	TailscaleInstance     string
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

func GetStatus(ctx context.Context, store IncusTenantStore, reference string) (Status, error) {
	return GetStatusWithTopology(ctx, store, nil, TopologyRequest{}, reference)
}

func GetStatusWithTopology(ctx context.Context, store IncusTenantStore, topologyStore TopologyStore, topologyRequest TopologyRequest, reference string) (Status, error) {
	ref, err := naming.ParseTenantRef(reference)
	if err != nil {
		return Status{}, err
	}
	tenants, err := List(ctx, store)
	if err != nil {
		return Status{}, err
	}
	for _, summary := range tenants {
		if summary.Tenant == ref.Tenant {
			status := Status{
				Summary: summary,
				Shares:  shareHealth(summary, tenants),
				Checks: []Check{
					{Name: "metadata", Status: "ok", Detail: "Sandcastle tenant metadata is present"},
					{Name: "cidr", Status: checkPresent(summary.PrivateCIDR), Detail: summary.PrivateCIDR},
					{Name: "dns", Status: checkPresent(summary.DNSSuffix), Detail: summary.DNSSuffix},
					tailscaleRouteCheck(summary),
				},
			}
			if topologyStore != nil {
				topologyRequest.IncusProject = summary.IncusName
				topologyRequest.InfraProject = naming.TenantInfraIncusProjectName(summary.IncusName)
				topologyRequest.DNSSuffix = summary.DNSSuffix
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
	return Status{}, fmt.Errorf("Sandcastle tenant %s not found", ref.String())
}

func shareHealth(summary Summary, tenants []Summary) ShareHealth {
	health := ShareHealth{}
	localByID := map[string]string{}
	for _, storageShare := range summary.StorageShares {
		id := storageShare.SourceTenant + "/" + storageShare.SourceProject + "/" + storageShare.Name
		if storageShare.SourceTenant == summary.Tenant {
			health.OutboundShareCount++
			continue
		}
		state := recipientStateForTenant(storageShare, summary.Tenant)
		localByID[id] = state
		if state == "accepted" {
			health.InboundAcceptedCount++
		}
	}
	for _, source := range tenants {
		if source.Tenant == summary.Tenant {
			continue
		}
		for _, storageShare := range source.StorageShares {
			if !shareOfferedToTenant(storageShare, summary.Tenant) {
				continue
			}
			id := storageShare.SourceTenant + "/" + storageShare.SourceProject + "/" + storageShare.Name
			if localByID[id] == "" {
				health.PendingInboundOfferCount++
			}
		}
	}
	return health
}

func shareOfferedToTenant(storageShare meta.TenantStorageShare, tenant string) bool {
	for _, recipient := range storageShare.Recipients {
		if recipient.Tenant == tenant {
			return true
		}
	}
	return false
}

func recipientStateForTenant(storageShare meta.TenantStorageShare, tenant string) string {
	for _, recipient := range storageShare.Recipients {
		if recipient.Tenant == tenant {
			return recipient.State
		}
	}
	return ""
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

// TopologyChecks returns stable tenant resource checks for status and diagnostics output.
func TopologyChecks(topology Topology) []Check {
	checks := []Check{
		{Name: "network:sc-private", Status: presentStatus(topology.PrivateNetworkPresent)},
	}
	for _, volume := range []string{HomeVolumeName, WorkspaceVolumeName, CAVolumeName} {
		checks = append(checks, Check{Name: "volume:" + volume, Status: presentStatus(topology.DurableVolumes[volume])})
	}
	for _, sidecar := range []string{topology.TailscaleInstance, DNSName} {
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
