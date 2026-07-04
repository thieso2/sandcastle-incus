package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/thieso2/sandcastle-incus/internal/incusx"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

// v2DefaultMachineImage is the stock cloud image v2 machines launch from: the
// /cloud variant carries cloud-init, which applies the project default profile
// (login user + SSH key + sshd). The plain variant would boot without any user.
const v2DefaultMachineImage = "images:debian/13/cloud"

// v2TenantSummary resolves the current tenant against the remote and reports
// whether it is a v2 tenant (per-project Incus projects, freeform machines).
func v2TenantSummary(ctx context.Context, config commandConfig) (tenant.Summary, bool) {
	name := strings.TrimSpace(config.adminConfig.Tenant)
	if name == "" || config.tenantStore == nil {
		return tenant.Summary{}, false
	}
	tenants, err := tenant.List(ctx, config.tenantStore)
	if err != nil {
		return tenant.Summary{}, false
	}
	for _, candidate := range tenants {
		if candidate.Tenant == name && candidate.Version == 2 {
			return candidate, true
		}
	}
	return tenant.Summary{}, false
}

// parseV2MachineReference splits "[tenant/][project:]machine" for a v2 create.
// The tenant part must match the current tenant (cross-tenant creates go
// through admin tooling); the project defaults to the configured Current
// Project, then to "default".
func parseV2MachineReference(reference string, currentTenant string, currentProject string) (project string, machine string, err error) {
	reference = strings.TrimSpace(reference)
	if tenantPart, rest, ok := strings.Cut(reference, "/"); ok {
		if strings.TrimSpace(tenantPart) != currentTenant {
			return "", "", fmt.Errorf("tenant %q does not match the current tenant %q", tenantPart, currentTenant)
		}
		reference = rest
	}
	project = strings.TrimSpace(currentProject)
	if projectPart, rest, ok := strings.Cut(reference, ":"); ok {
		project = strings.TrimSpace(projectPart)
		reference = rest
	}
	if project == "" {
		project = naming.DefaultProjectName
	}
	machine = strings.TrimSpace(reference)
	if machine == "" {
		return "", "", fmt.Errorf("machine name is required")
	}
	if err := naming.ValidateProjectName(project); err != nil {
		return "", "", err
	}
	if err := naming.ValidateMachineName(machine); err != nil {
		return "", "", err
	}
	return project, machine, nil
}

type createV2Options struct {
	Image  string
	VM     bool
	DryRun bool
}

func runCreateMachineV2(ctx context.Context, config commandConfig, opts *rootOptions, summary tenant.Summary, reference string, options createV2Options) error {
	project, machine, err := parseV2MachineReference(reference, summary.Tenant, config.adminConfig.Project)
	if err != nil {
		return err
	}
	image := strings.TrimSpace(options.Image)
	if image == "" {
		image = v2DefaultMachineImage
	}
	request := incusx.CreateMachineV2Request{
		IncusProject: summary.V2IncusProjectName(project),
		Name:         machine,
		Image:        image,
		VM:           options.VM,
	}
	if options.DryRun {
		payload := incusx.CreateMachineV2Result{Name: machine, Type: machineTypeLabel(options.VM), Project: request.IncusProject, Image: image}
		return writeOutput(config.stdout, opts.output, formatCreateMachineV2(summary, project, payload, true), payload)
	}
	result, err := config.tenantCreator.CreateMachineV2(ctx, request)
	if err != nil {
		return err
	}
	return writeOutput(config.stdout, opts.output, formatCreateMachineV2(summary, project, result, false), result)
}

func machineTypeLabel(vm bool) string {
	if vm {
		return "virtual-machine"
	}
	return "container"
}

func formatCreateMachineV2(summary tenant.Summary, project string, result incusx.CreateMachineV2Result, dryRun bool) string {
	var builder strings.Builder
	verb := "created"
	if dryRun {
		verb = "would be created"
	}
	fmt.Fprintf(&builder, "Machine %s %s (%s, project %s, image %s).\n", result.Name, verb, result.Type, project, result.Image)
	fqdn := result.Name + "." + summary.DNSSuffix
	if dryRun {
		fmt.Fprintf(&builder, "DNS: %s (auto-registers after boot)", fqdn)
		return builder.String()
	}
	if result.PrivateIP != "" {
		fmt.Fprintf(&builder, "IP: %s   DNS: %s (auto-registers in ~30s)\n", result.PrivateIP, fqdn)
		fmt.Fprintf(&builder, "SSH: ssh dev@%s   (cloud-init may still be installing sshd)", result.PrivateIP)
	} else {
		fmt.Fprintf(&builder, "Still booting — no IP leased yet. Watch it with: sc list\n")
		fmt.Fprintf(&builder, "DNS: %s (auto-registers after boot)", fqdn)
	}
	return builder.String()
}
