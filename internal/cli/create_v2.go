package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

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

// runConnectV2 implements `sc connect` (alias `c`) for v2 tenants: create the
// machine if it doesn't exist, start it if it is stopped, wait for sshd, then
// open an SSH session as the profile login user with the login SSH key.
func runConnectV2(ctx context.Context, config commandConfig, summary tenant.Summary, reference string, command []string) error {
	project, machineName, err := parseV2MachineReference(reference, summary.Tenant, config.adminConfig.Project)
	if err != nil {
		return err
	}
	ensured, err := config.tenantCreator.EnsureMachineV2(ctx, incusx.CreateMachineV2Request{
		IncusProject: summary.V2IncusProjectName(project),
		Name:         machineName,
		Image:        v2DefaultMachineImage,
	})
	if err != nil {
		return err
	}
	switch {
	case ensured.Created:
		fmt.Fprintf(config.stdout, "Machine %s created (project %s).\n", machineName, project)
	case ensured.Started:
		fmt.Fprintf(config.stdout, "Machine %s started.\n", machineName)
	}
	if ensured.PrivateIP == "" {
		return fmt.Errorf("machine %s has no IP yet — still booting; retry in a few seconds (watch with: sc list)", machineName)
	}
	// A fresh machine needs cloud-init to install and start sshd.
	sshDeadline := time.Now().Add(120 * time.Second)
	for !probeSSHPort(ensured.PrivateIP, 3*time.Second) {
		if !time.Now().Before(sshDeadline) {
			return fmt.Errorf("machine %s (%s) did not open SSH within 2 minutes — cloud-init may still be running", machineName, ensured.PrivateIP)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	sshKey, err := prepareLoginSSHKey(loginSSHKeyRequest{})
	if err != nil {
		return err
	}
	privateKeyPath := strings.TrimSuffix(sshKey.PublicKeyPath, ".pub")
	sshArgs := []string{
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "IdentitiesOnly=yes",
		"-i", privateKeyPath,
		ensured.LoginUser + "@" + ensured.PrivateIP,
	}
	sshArgs = append(sshArgs, command...)
	fmt.Fprintf(config.stdout, "Connecting: ssh %s@%s\n", ensured.LoginUser, ensured.PrivateIP)
	sshCmd := exec.CommandContext(ctx, "ssh", sshArgs...)
	sshCmd.Stdin = osStdinFor(config)
	sshCmd.Stdout = config.stdout
	sshCmd.Stderr = config.stderr
	return sshCmd.Run()
}

// osStdinFor hands the real stdin to interactive subprocesses when the command
// config carries os.Stdin (the normal CLI case); test configs keep their reader.
func osStdinFor(config commandConfig) io.Reader {
	if config.stdin != nil {
		return config.stdin
	}
	return os.Stdin
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
	loginUser := result.LoginUser
	if loginUser == "" {
		loginUser = tenant.DefaultV2UnixUser
	}
	if result.PrivateIP != "" {
		fmt.Fprintf(&builder, "IP: %s   DNS: %s (auto-registers in ~30s)\n", result.PrivateIP, fqdn)
		fmt.Fprintf(&builder, "SSH: ssh %s@%s   (cloud-init may still be installing sshd)", loginUser, result.PrivateIP)
	} else {
		fmt.Fprintf(&builder, "Still booting — no IP leased yet. Watch it with: sc list\n")
		fmt.Fprintf(&builder, "DNS: %s (auto-registers after boot)", fqdn)
	}
	return builder.String()
}
