package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
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
//
// The error is separate from the bool on purpose: "the remote said no such
// tenant" and "the remote could not be reached" are different answers, and
// collapsing them is how an unreachable host came to be reported as
// `Sandcastle tenant <name> not found`. Callers that genuinely do not care
// (best-effort decoration) discard it explicitly.
func v2TenantSummary(ctx context.Context, config commandConfig) (tenant.Summary, bool, error) {
	name := strings.TrimSpace(config.adminConfig.Tenant)
	if name == "" || config.tenantStore == nil {
		return tenant.Summary{}, false, nil
	}
	// Several installs can share one Incus daemon (every sidecar's Incus Reach
	// lands on the same host API), so a same-named tenant may exist once per
	// install. Scope the lookup to the install the current remote belongs to.
	tenants, err := tenant.ListForPrefix(ctx, config.tenantStore, installPrefixForRemote(config, name))
	if err != nil {
		return tenant.Summary{}, false, err
	}
	for _, candidate := range tenants {
		if candidate.Tenant == name {
			return candidate, true, nil
		}
	}
	return tenant.Summary{}, false, nil
}

// scopedListTenants lists tenant summaries scoped to the install the
// configured remote belongs to. Several installs can share one Incus daemon
// (every sidecar's Incus Reach lands on the same host API) and the shared
// client certificate sees every enrolled install's projects, so a same-named
// tenant may exist once per install — matching by tenant name alone can land
// on the wrong install (e.g. `sc list` right after `sc create` showing the
// OTHER install's empty machine set). Unscoped fallback (empty prefix) when
// the remote name has another shape (admin remotes, v1).
func scopedListTenants(ctx context.Context, config commandConfig, tenantName string) ([]tenant.Summary, error) {
	return tenant.ListForPrefix(ctx, config.tenantStore, installPrefixForRemote(config, tenantName))
}

// installPrefixForRemote resolves which install's projects the CLI is pointed
// at. It prefers the active remote's PINNED PROJECT (robust for URL-based remote
// names like sc-obelix-thieso2-dev, which don't encode the install prefix),
// deriving the prefix from <prefix>-<tenant>-<project>, and falls back to
// inverting a legacy sc-<prefix>-<tenant> remote name. Without this scoping,
// two installs sharing a tenant name (same GitHub user) collapse together and
// switching the remote fails to switch what sc shows.
func installPrefixForRemote(config commandConfig, tenantName string) string {
	if prefix := installPrefixFromProject(scconfig.SharedIncusRemoteProject(config.adminConfig.Remote), tenantName); prefix != "" {
		return prefix
	}
	return installPrefixFromRemoteName(config.adminConfig.Remote, tenantName)
}

// installPrefixFromProject extracts the install prefix from a pinned project
// name shaped <prefix>-<tenant>-<project> (or <prefix>-<tenant>): the segment
// before "-<tenant>". Returns "" when the project does not belong to tenantName.
func installPrefixFromProject(project string, tenantName string) string {
	project = strings.TrimSpace(project)
	tenantName = strings.TrimSpace(tenantName)
	if project == "" || tenantName == "" {
		return ""
	}
	marker := "-" + tenantName
	idx := strings.Index(project, marker)
	if idx <= 0 {
		return ""
	}
	rest := project[idx+len(marker):]
	if rest != "" && !strings.HasPrefix(rest, "-") {
		return "" // "-<tenant>" is a substring, not a real boundary
	}
	return project[:idx]
}

// installPrefixFromRemoteName maps the enrolled remote to its install's
// project prefix. The authoritative source is the remote's pinned project in
// the shared incus config (installPrefixFromRemotePin) — URL-named remotes
// ("sc-<install-label>", usertrust.RemoteNameForAuthHostname) carry no prefix
// in the name at all, and without the pin every lookup silently went unscoped,
// resurrecting the cross-install shadowing this scoping exists to prevent
// (seen live on majestix: `sc list` under install A showed install B's
// machines). Fallback: invert the legacy usertrust.RemoteInstallName shape
// "sc-<prefix>-<tenant>" / "sc-<tenant>". Returns "" (no scoping) when
// neither source identifies the install.
func installPrefixFromRemoteName(remote string, tenantName string) string {
	remote = strings.TrimSpace(remote)
	tenantName = strings.TrimSpace(tenantName)
	if tenantName == "" {
		return ""
	}
	if prefix := installPrefixFromRemotePin(remote, tenantName); prefix != "" {
		return prefix
	}
	if remote == "sc-"+tenantName {
		return naming.DefaultIncusProjectPrefix
	}
	if rest, ok := strings.CutPrefix(remote, "sc-"); ok {
		if prefix, ok := strings.CutSuffix(rest, "-"+tenantName); ok && prefix != "" {
			return prefix
		}
	}
	return ""
}

// parseV2MachineReference splits "[[dns-suffix:]project:]machine" (ADR-0020)
// into its parts. Colon count selects scope: 0 colons = machine only,
// 1 = project:machine, 2 = dns-suffix:project:machine (the leftmost part names
// the install by its DNS suffix). The project defaults to the configured
// Current Project, then to "default". A returned dnsSuffix of "" means "the
// current install". currentTenant is unused now that the legacy "tenant/"
// prefix is dropped (ADR-0020); it is retained in the signature for callers.
func parseV2MachineReference(reference string, currentTenant string, currentProject string) (dnsSuffix string, project string, machine string, err error) {
	_ = currentTenant
	reference = strings.TrimSpace(reference)
	project = strings.TrimSpace(currentProject)
	// Split the colon-separated tail into [dns-suffix :] [project :] machine.
	parts := strings.Split(reference, ":")
	switch len(parts) {
	case 1:
		machine = parts[0]
	case 2:
		project = strings.TrimSpace(parts[0])
		machine = parts[1]
	case 3:
		dnsSuffix = strings.TrimSpace(parts[0])
		project = strings.TrimSpace(parts[1])
		machine = parts[2]
	default:
		return "", "", "", fmt.Errorf("invalid machine reference %q: expected [[dns-suffix:]project:]machine", reference)
	}
	if project == "" {
		project = naming.DefaultProjectName
	}
	machine = strings.TrimSpace(machine)
	if machine == "" {
		return "", "", "", fmt.Errorf("machine name is required")
	}
	if dnsSuffix != "" {
		if err := naming.ValidateInstallSuffix(dnsSuffix); err != nil {
			return "", "", "", err
		}
	}
	if err := naming.ValidateProjectName(project); err != nil {
		return "", "", "", err
	}
	if err := naming.ValidateMachineName(machine); err != nil {
		return "", "", "", err
	}
	return dnsSuffix, project, machine, nil
}

// resolveV2MachineReference parses "[[dns-suffix:]project:]machine" (ADR-0020)
// and verifies the project actually exists in the tenant — otherwise a mistyped
// project surfaces as a raw Incus "User does not have permission for project …"
// from the nonexistent project's name. A machine part that matches an existing
// project usually means the reference was written backwards; suggest the swap.
func resolveV2MachineReference(summary tenant.Summary, reference string, currentProject string) (project string, machine string, err error) {
	dnsSuffix, project, machine, err := parseV2MachineReference(reference, summary.Tenant, currentProject)
	if err != nil {
		return "", "", err
	}
	// An explicit install suffix that matches the current install is a no-op;
	// one that names a different install requires switching to that install's
	// remote, which is not wired inline yet (ADR-0020; see implementation-notes).
	currentInstall := strings.TrimSpace(summary.DNSSuffix)
	if dnsSuffix != "" && dnsSuffix != currentInstall {
		return "", "", fmt.Errorf("reference %q addresses install %q, but the current remote is install %q; "+
			"inline cross-install addressing is not available yet — select that install's remote first",
			reference, dnsSuffix, currentInstall)
	}
	if _, ok := findProject(summary, project); !ok {
		names := make([]string, 0, len(summary.Projects))
		for _, candidate := range summary.Projects {
			names = append(names, candidate.Name)
		}
		hint := ""
		if _, swapped := findProject(summary, machine); swapped {
			hint = fmt.Sprintf("\nThe reference is [[dns-suffix:]project:]machine — did you mean %q?", machine+":"+project)
		}
		if hint == "" && localRemoteExists(project) {
			// e.g. `sc c obelix-sc:dev` — `obelix-sc` is a remote name, not a
			// project. Address another install as dns-suffix:project:machine.
			hint = fmt.Sprintf("\n%q is an incus remote, not a project — reach another install with dns-suffix:project:machine.", project)
		}
		return "", "", fmt.Errorf("project %q not found in tenant %s (projects: %s).%s\nCreate it with: sc project create %s",
			project, summary.Tenant, strings.Join(names, ", "), hint, project)
	}
	return project, machine, nil
}

// v2ReferenceHasProject reports whether the reference names its project
// explicitly ("[tenant/]project:machine") rather than leaving it to be inferred.
func v2ReferenceHasProject(reference string) bool {
	reference = strings.TrimSpace(reference)
	if _, rest, ok := strings.Cut(reference, "/"); ok {
		reference = rest
	}
	return strings.Contains(reference, ":")
}

// resolveV2MachineTarget resolves a reference to a machine that must already
// exist. An explicit "project:machine" is taken at its word. A bare machine
// name is looked up across every project of the tenant instead of assuming the
// Current Project: one hit resolves silently, several ask which one is meant.
// No hit falls back to the inferred project so the caller's own "not found"
// names the project it looked in.
func resolveV2MachineTarget(ctx context.Context, config commandConfig, summary tenant.Summary, reference string) (project string, machine string, err error) {
	project, machine, err = resolveV2MachineReference(summary, reference, config.adminConfig.Project)
	if err != nil || v2ReferenceHasProject(reference) {
		return project, machine, err
	}
	projects, err := v2MachineProjects(ctx, config, summary, machine)
	if err != nil {
		return "", "", err
	}
	switch len(projects) {
	case 0:
		return project, machine, nil
	case 1:
		return projects[0], machine, nil
	}
	qualified := make([]string, 0, len(projects))
	for _, candidate := range projects {
		qualified = append(qualified, candidate+":"+machine)
	}
	if !isTerminalInput(config) {
		return "", "", fmt.Errorf("machine %q exists in %d projects (%s); name the one you mean as project:machine",
			machine, len(projects), strings.Join(qualified, ", "))
	}
	choice, err := promptChoice(config, fmt.Sprintf("Machine %q exists in %d projects:", machine, len(projects)), qualified)
	if err != nil {
		return "", "", err
	}
	return projects[choice], machine, nil
}

// v2MachineProjects returns, sorted, the projects of the tenant that hold a
// machine with the given name.
func v2MachineProjects(ctx context.Context, config commandConfig, summary tenant.Summary, machine string) ([]string, error) {
	if config.machineStore == nil {
		return nil, fmt.Errorf("machine metadata store is not configured")
	}
	machines, err := config.machineStore.ListMachines(ctx, summary)
	if err != nil {
		return nil, err
	}
	projects := []string{}
	for _, candidate := range machines {
		if candidate.Name == machine {
			projects = append(projects, candidate.Project)
		}
	}
	sort.Strings(projects)
	return projects, nil
}

type createV2Options struct {
	Image  string
	VM     bool
	DryRun bool
}

func runCreateMachineV2(ctx context.Context, config commandConfig, opts *rootOptions, summary tenant.Summary, reference string, options createV2Options) error {
	project, machine, err := resolveV2MachineReference(summary, reference, config.adminConfig.Project)
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
func runConnectV2(ctx context.Context, config commandConfig, summary tenant.Summary, reference string, command []string, vm bool) error {
	dialed, err := dialV2Machine(ctx, config, summary, reference, vm)
	if err != nil {
		return err
	}
	sshArgs := dialed.sshArgs
	// ssh joins its trailing arguments with spaces into ONE remote command
	// string that the remote shell re-splits, so argv must be shell-quoted here
	// or `sh -c 'echo hi'` arrives as `sh -c echo hi`.
	if line := remoteCommandLine(command); line != "" {
		sshArgs = append(sshArgs, line)
	}
	fmt.Fprintf(config.stdout, "Connecting: ssh %s@%s\n", dialed.loginUser, dialed.privateIP)
	sshCmd := exec.CommandContext(ctx, "ssh", sshArgs...)
	sshCmd.Stdin = osStdinFor(config)
	sshCmd.Stdout = config.stdout
	sshCmd.Stderr = config.stderr
	return sshCmd.Run()
}

// dialedV2Machine carries everything needed to run ssh against a resolved,
// running machine: the base ssh argv (options + login target, no remote command
// yet) plus the login user and IP for messaging.
type dialedV2Machine struct {
	sshArgs   []string
	loginUser string
	privateIP string
	project   string
	machine   string
}

// dialV2Machine resolves a machine reference, ensures the machine exists and is
// up (creating it if absent, like `sc connect`), waits for sshd, and builds the
// ssh argv with strict host-key checking. Callers append their own remote
// command (or feed one on stdin). Shared by `sc connect` and `sc fix`.
func dialV2Machine(ctx context.Context, config commandConfig, summary tenant.Summary, reference string, vm bool) (dialedV2Machine, error) {
	project, machineName, err := resolveV2MachineReference(summary, reference, config.adminConfig.Project)
	if err != nil {
		return dialedV2Machine{}, err
	}
	ensured, err := config.tenantCreator.EnsureMachineV2(ctx, incusx.CreateMachineV2Request{
		IncusProject: summary.V2IncusProjectName(project),
		Name:         machineName,
		Image:        v2DefaultMachineImage,
		VM:           vm,
	})
	if err != nil {
		return dialedV2Machine{}, err
	}
	switch {
	case ensured.Created:
		fmt.Fprintf(config.stdout, "Machine %s created (project %s).\n", machineName, project)
	case ensured.Started:
		fmt.Fprintf(config.stdout, "Machine %s started.\n", machineName)
	}
	if ensured.PrivateIP == "" {
		return dialedV2Machine{}, fmt.Errorf("machine %s has no IP yet — still booting; retry in a few seconds (watch with: sc list)", machineName)
	}
	// A fresh machine needs cloud-init to install and start sshd. VMs take
	// longer: image download + firmware/kernel boot before cloud-init even runs.
	sshWait := 120 * time.Second
	if vm {
		sshWait = 240 * time.Second
	}
	sshDeadline := time.Now().Add(sshWait)
	for !probeSSHPort(ensured.PrivateIP, 3*time.Second) {
		if !time.Now().Before(sshDeadline) {
			return dialedV2Machine{}, fmt.Errorf("machine %s (%s) did not open SSH within %s — cloud-init may still be running", machineName, ensured.PrivateIP, sshWait)
		}
		select {
		case <-ctx.Done():
			return dialedV2Machine{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	sshKey, err := prepareLoginSSHKey(loginSSHKeyRequest{})
	if err != nil {
		return dialedV2Machine{}, err
	}
	privateKeyPath := strings.TrimSuffix(sshKey.PublicKeyPath, ".pub")
	// Record the machine's authoritative host key under the names it answers
	// at, then dial its IP but check the key against the name (HostKeyAlias).
	// Names are stable; private IPs are recycled leases. With the true key
	// already on disk we can demand StrictHostKeyChecking=yes, so a rebuilt
	// machine never trips the MITM warning and a real impostor always does.
	names := v2MachineNames(summary, project, machineName)
	sshArgs := []string{"-o", "IdentitiesOnly=yes", "-i", privateKeyPath}
	if len(names) > 0 && ensureV2HostKey(ctx, config, summary, project, machineName, ensured.PrivateIP, ensured.PrivateCIDR) {
		sshArgs = append(sshArgs,
			"-o", "HostKeyAlias="+names[0],
			"-o", "StrictHostKeyChecking=yes",
			"-o", "CheckHostIP=no",
		)
	} else {
		sshArgs = append(sshArgs, "-o", "StrictHostKeyChecking=accept-new")
	}
	sshArgs = append(sshArgs, ensured.LoginUser+"@"+ensured.PrivateIP)
	return dialedV2Machine{
		sshArgs:   sshArgs,
		loginUser: ensured.LoginUser,
		privateIP: ensured.PrivateIP,
		project:   project,
		machine:   machineName,
	}, nil
}

// remoteCommandLine renders argv as a single command line for ssh, which
// concatenates its trailing arguments with spaces and lets the remote login
// shell re-split the result. Without quoting, `sc c web -- sh -c 'id -un'`
// reaches the machine as `sh -c id -un` and runs `id` with no arguments.
//
// A lone argument passes through verbatim so `sc c web -- 'ls -l /tmp'` stays a
// shell snippet, matching the v1 connect path (incusx.remoteShellCommand).
func remoteCommandLine(command []string) string {
	switch len(command) {
	case 0:
		return ""
	case 1:
		return strings.TrimSpace(command[0])
	}
	quoted := make([]string, 0, len(command))
	for _, arg := range command {
		quoted = append(quoted, shellQuoteArg(arg))
	}
	return strings.Join(quoted, " ")
}

// shellQuoteArg single-quotes a value for a POSIX shell. An embedded single
// quote is escaped by closing the quoted run, emitting an escaped quote, and
// reopening it.
func shellQuoteArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
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
	// Canonical Machine Private Hostname; the default project also answers at
	// the short alias (ADR-0018).
	fqdn := result.Name + "." + project + "." + summary.DNSSuffix
	if project == naming.DefaultProjectName {
		fqdn += " (also: " + result.Name + "." + summary.DNSSuffix + ")"
	}
	if dryRun {
		fmt.Fprintf(&builder, "DNS: %s (auto-registers after boot)", fqdn)
		return builder.String()
	}
	loginUser := result.LoginUser
	if loginUser == "" {
		loginUser = tenant.DefaultV2UnixUser
	}
	if result.PrivateIP != "" {
		fmt.Fprintf(&builder, "IP: %s   DNS: %s (auto-registers within seconds)\n", result.PrivateIP, fqdn)
		fmt.Fprintf(&builder, "SSH: ssh %s@%s   (cloud-init may still be installing sshd)", loginUser, result.PrivateIP)
	} else {
		fmt.Fprintf(&builder, "Still booting — no IP leased yet. Watch it with: sc list\n")
		fmt.Fprintf(&builder, "DNS: %s (auto-registers after boot)", fqdn)
	}
	return builder.String()
}

// requireV2Tenant resolves the current tenant. v1 is gone, so every Sandcastle
// tenant is v2 and a lookup miss simply means the tenant does not exist — there
// is no other shape it could be.
func requireV2Tenant(ctx context.Context, config commandConfig) (tenant.Summary, error) {
	summary, ok, err := v2TenantSummary(ctx, config)
	if err != nil {
		// The lookup never got an answer — say so, instead of turning an
		// unreachable remote into a claim about the tenant.
		return tenant.Summary{}, err
	}
	if !ok {
		name := strings.TrimSpace(config.adminConfig.Tenant)
		if name == "" {
			return tenant.Summary{}, fmt.Errorf("a tenant is required (set one with `sc config set tenant <name>` or log in)")
		}
		return tenant.Summary{}, fmt.Errorf("Sandcastle tenant %s not found", name)
	}
	return summary, nil
}
