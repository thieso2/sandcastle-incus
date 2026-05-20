package tailscale

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type StatusRequest struct {
	Reference string
}

type StatusPlan struct {
	Reference    string          `json:"reference"`
	Project      project.Summary `json:"project"`
	InstanceName string          `json:"instanceName"`
	Command      []string        `json:"command"`
}

type StatusResult struct {
	Reference string          `json:"reference"`
	Project   project.Summary `json:"project"`
	Tailscale meta.Tailscale  `json:"tailscale"`
}

type DownRequest struct {
	Reference string
}

type DownPlan struct {
	Reference    string          `json:"reference"`
	Project      project.Summary `json:"project"`
	InstanceName string          `json:"instanceName"`
	Command      []string        `json:"command"`
}

func PlanStatus(ctx context.Context, admin config.Admin, store project.IncusProjectStore, request StatusRequest) (StatusPlan, error) {
	summary, reference, err := projectSummary(ctx, admin, store, request.Reference)
	if err != nil {
		return StatusPlan{}, err
	}
	return StatusPlan{
		Reference:    reference,
		Project:      summary,
		InstanceName: project.TailscaleName,
		Command:      []string{"tailscale", "status", "--json"},
	}, nil
}

func PlanDown(ctx context.Context, admin config.Admin, store project.IncusProjectStore, request DownRequest) (DownPlan, error) {
	summary, reference, err := projectSummary(ctx, admin, store, request.Reference)
	if err != nil {
		return DownPlan{}, err
	}
	return DownPlan{
		Reference:    reference,
		Project:      summary,
		InstanceName: project.TailscaleName,
		Command:      []string{"tailscale", "down"},
	}, nil
}

func ParseStatus(reference string, summary project.Summary, data []byte, now time.Time) (StatusResult, error) {
	var payload statusPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return StatusResult{}, fmt.Errorf("parse tailscale status JSON: %w", err)
	}
	state := payload.BackendState
	if state == "" {
		state = "unknown"
	}
	tailnet := payload.CurrentTailnet.Name
	if tailnet == "" {
		tailnet = payload.CurrentTailnet.MagicDNSSuffix
	}
	hostname := payload.Self.HostName
	if hostname == "" {
		hostname = strings.TrimSuffix(payload.Self.DNSName, ".")
	}
	return StatusResult{
		Reference: reference,
		Project:   summary,
		Tailscale: meta.Tailscale{
			State:            state,
			Tailnet:          tailnet,
			Hostname:         hostname,
			AdvertisedRoutes: append([]string{}, payload.Self.PrimaryRoutes...),
			TailscaleIPs:     append([]string{}, payload.Self.TailscaleIPs...),
			LastCheckedAt:    now.UTC().Format(time.RFC3339),
		},
	}, nil
}

func projectSummary(ctx context.Context, admin config.Admin, store project.IncusProjectStore, reference string) (project.Summary, string, error) {
	if err := admin.Validate(); err != nil {
		return project.Summary{}, "", err
	}
	ref, err := naming.ParseProjectRefWithDefaultOwner(reference, admin.Owner)
	if err != nil {
		return project.Summary{}, "", err
	}
	summary, err := findProject(ctx, store, ref)
	if err != nil {
		return project.Summary{}, "", err
	}
	return summary, ref.String(), nil
}

type statusPayload struct {
	BackendState   string `json:"BackendState"`
	CurrentTailnet struct {
		Name           string `json:"Name"`
		MagicDNSSuffix string `json:"MagicDNSSuffix"`
	} `json:"CurrentTailnet"`
	Self struct {
		DNSName       string   `json:"DNSName"`
		HostName      string   `json:"HostName"`
		TailscaleIPs  []string `json:"TailscaleIPs"`
		PrimaryRoutes []string `json:"PrimaryRoutes"`
	} `json:"Self"`
}
