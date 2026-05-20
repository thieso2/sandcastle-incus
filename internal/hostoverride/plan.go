package hostoverride

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	"github.com/thieso2/sandcastle-incus/internal/project"
)

type AddRequest struct {
	Reference string
	Hostname  string
}

type AddPlan struct {
	Reference         string          `json:"reference"`
	Project           project.Summary `json:"project"`
	Sandbox           meta.Sandbox    `json:"sandbox"`
	InstanceName      string          `json:"instanceName"`
	StoragePool       string          `json:"storagePool"`
	CAVolume          string          `json:"caVolume"`
	Hostname          string          `json:"hostname"`
	IPAddress         string          `json:"ipAddress"`
	ExtraSANs         []string        `json:"extraSANs"`
	HostsEntry        HostsEntry      `json:"hostsEntry"`
	TrustWarning      string          `json:"trustWarning"`
	RequiresReissue   bool            `json:"requiresReissue"`
	RequiresHostsEdit bool            `json:"requiresHostsEdit"`
}

type HostsEntry struct {
	BeginLine string `json:"beginLine"`
	Line      string `json:"line"`
	EndLine   string `json:"endLine"`
}

type SandboxStore interface {
	FindSandbox(ctx context.Context, project project.Summary, name string) (meta.Sandbox, error)
}

type Manager interface {
	Add(context.Context, AddPlan) error
}

func PlanAdd(ctx context.Context, admin config.Admin, projectStore project.IncusProjectStore, sandboxStore SandboxStore, request AddRequest) (AddPlan, error) {
	if err := admin.Validate(); err != nil {
		return AddPlan{}, err
	}
	projectRef, sandboxName, err := parseSandboxRef(request.Reference)
	if err != nil {
		return AddPlan{}, err
	}
	hostname, err := normalizeExactHostname(request.Hostname)
	if err != nil {
		return AddPlan{}, err
	}
	summary, err := findProject(ctx, projectStore, projectRef)
	if err != nil {
		return AddPlan{}, err
	}
	if sandboxStore == nil {
		return AddPlan{}, fmt.Errorf("sandbox metadata store is required")
	}
	sandbox, err := sandboxStore.FindSandbox(ctx, summary, sandboxName)
	if err != nil {
		return AddPlan{}, err
	}
	if sandbox.PrivateIP == "" {
		return AddPlan{}, fmt.Errorf("sandbox %s has no private IP", request.Reference)
	}
	return AddPlan{
		Reference:         request.Reference,
		Project:           summary,
		Sandbox:           sandbox,
		InstanceName:      "sc-" + sandboxName,
		StoragePool:       admin.StoragePool,
		CAVolume:          project.CAVolumeName,
		Hostname:          hostname,
		IPAddress:         sandbox.PrivateIP,
		ExtraSANs:         []string{hostname},
		HostsEntry:        RenderHostsEntry(request.Reference, hostname, sandbox.PrivateIP),
		TrustWarning:      "Trust the project CA before relying on HTTPS for this host override.",
		RequiresReissue:   true,
		RequiresHostsEdit: true,
	}, nil
}

func RenderHostsEntry(reference string, hostname string, ipAddress string) HostsEntry {
	id := strings.ToLower(strings.TrimSpace(reference)) + " " + strings.ToLower(strings.TrimSpace(hostname))
	return HostsEntry{
		BeginLine: "# sandcastle host-override begin " + id,
		Line:      strings.TrimSpace(ipAddress) + " " + strings.ToLower(strings.TrimSpace(hostname)),
		EndLine:   "# sandcastle host-override end " + id,
	}
}

func normalizeExactHostname(value string) (string, error) {
	hostname := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if hostname == "" {
		return "", fmt.Errorf("hostname is required")
	}
	if strings.Contains(hostname, "*") {
		return "", fmt.Errorf("wildcard host overrides are not supported")
	}
	if strings.Contains(hostname, "/") || net.ParseIP(hostname) != nil {
		return "", fmt.Errorf("host override must be an exact DNS hostname")
	}
	labels := strings.Split(hostname, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("host override must be a fully qualified hostname")
	}
	for _, label := range labels {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("invalid hostname %q", value)
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return "", fmt.Errorf("invalid hostname %q", value)
		}
	}
	return hostname, nil
}

func parseSandboxRef(value string) (naming.ProjectRef, string, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 3 {
		return naming.ProjectRef{}, "", fmt.Errorf("sandbox reference must be owner/project/name")
	}
	projectRef, err := naming.ParseProjectRef(parts[0] + "/" + parts[1])
	if err != nil {
		return naming.ProjectRef{}, "", err
	}
	if err := (naming.ProjectRef{Owner: parts[2], Project: "placeholder"}).Validate(); err != nil {
		return naming.ProjectRef{}, "", fmt.Errorf("invalid sandbox name %q", parts[2])
	}
	return projectRef, parts[2], nil
}

func findProject(ctx context.Context, store project.IncusProjectStore, ref naming.ProjectRef) (project.Summary, error) {
	projects, err := project.List(ctx, store)
	if err != nil {
		return project.Summary{}, err
	}
	for _, summary := range projects {
		if summary.Owner == ref.Owner && summary.Name == ref.Project {
			return summary, nil
		}
	}
	return project.Summary{}, fmt.Errorf("Sandcastle project %s not found", ref.String())
}
