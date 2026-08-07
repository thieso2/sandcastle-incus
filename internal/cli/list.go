package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	scconfig "github.com/thieso2/sandcastle-incus/internal/config"
	"github.com/thieso2/sandcastle-incus/internal/incusx"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	tenant "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type listPayload struct {
	Tenant         tenant.Summary             `json:"tenant"`
	Remote         string                     `json:"remote,omitempty"`
	Project        string                     `json:"project,omitempty"`
	Machine        string                     `json:"machine,omitempty"`
	AllProjects    bool                       `json:"allProjects"`
	Machines       []meta.Machine             `json:"machines"`
	Unmanaged      []machine.UnmanagedMachine `json:"unmanaged,omitempty"`
	UnmanagedCount int                        `json:"unmanagedCount"`
}

// multiListPayload is what a `sc ls` whose install part globs returns: one
// section per enrolled install swept. Kept separate from listPayload so the
// single-install shape — which scripts already parse — is untouched.
type multiListPayload struct {
	RemotePattern string        `json:"remotePattern"`
	Remotes       []listPayload `json:"remotes"`
	// Warnings names installs the sweep could not read. A listing reports them
	// and shows the rest, rather than failing whole because one install of
	// several is down.
	Warnings []string `json:"warnings,omitempty"`
}

type tenantListPayload struct {
	Tenants []tenant.Summary `json:"tenants"`
}

type tenantResourcesPayload struct {
	Tenant    tenant.Summary             `json:"tenant"`
	Machines  []meta.Machine             `json:"machines"`
	Unmanaged []machine.UnmanagedMachine `json:"unmanaged,omitempty"`
}

func newListCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var allProjects bool
	command := &cobra.Command{
		Use:     "list [[remote:]project[:machine]]",
		Aliases: []string{"ls"},
		Short:   "List Sandcastle machines",
		Args:    cobra.MaximumNArgs(1),
		Long: `List Sandcastle machines in the current install's project.

The argument may carry a "<remote>:" prefix to list another enrolled install
without switching to it — e.g. "sc ls obelix:home" (project home on remote
obelix) or "sc ls obelix:" (that remote's default project). A leading part is
read as a remote only when it names an enrolled one; "sc remote list" shows
them.

Every part accepts shell-style wildcards:

  sc ls 'gbrain:*'        every machine in project gbrain
  sc ls -a 'g*:d*'        d* machines in every project matching g*
  sc ls -a '*:web-*'      the web-* machines of every project
  sc ls -a '*:*:*'        every machine on every enrolled install
  sc ls 'o*:gbrain:d*'    …scoped to the installs matching o*

Globbing installs needs all three parts spelled out: a two-part reference stays
[remote:]project or project:machine. Quote the pattern so the shell does not
expand it first.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			remoteOverride, project, machineName := splitListReference(optionalArg(args), localRemoteExists)
			request := listMachinesRequest{
				Project:     project,
				Machine:     machineName,
				AllProjects: allProjects,
			}
			if naming.IsPattern(remoteOverride) {
				payload, err := listMachinesAcrossRemotes(cmd.Context(), config, defaultRemoteFanout(), remoteOverride, request)
				if err != nil {
					return err
				}
				return writeOutput(config.stdout, opts.output, formatMultiMachineList(payload), payload)
			}
			runCfg := config
			if remoteOverride != "" && remoteOverride != strings.TrimSpace(config.adminConfig.Remote) {
				scoped, restore, err := listConfigForRemote(config, remoteOverride)
				if err != nil {
					return err
				}
				defer restore()
				runCfg = scoped
			}
			result, err := listMachines(cmd.Context(), runCfg, request)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatMachineList(result), result)
		},
	}
	command.Flags().BoolVarP(&allProjects, "all-projects", "a", false, "list machines across all projects")
	return command
}

func listTenants(ctx context.Context, store tenant.IncusTenantStore) ([]tenant.Summary, error) {
	return tenant.List(ctx, store)
}

func formatTenantList(tenants []tenant.Summary) string {
	if len(tenants) == 0 {
		return "No Sandcastle tenants found."
	}
	var builder strings.Builder
	for _, tenant := range tenants {
		fmt.Fprintf(&builder, "%s\t%s\t%s\n", tenant.Tenant, tenant.DNSSuffix, tenant.Status)
	}
	return strings.TrimRight(builder.String(), "\n")
}

type listMachinesRequest struct {
	Project     string
	Machine     string
	AllProjects bool
}

func optionalArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// splitListReference parses `sc ls` addressing: "[remote:]project[:machine]".
//
//	obelix:home     → ("obelix", "home", "")     remote obelix, project home
//	obelix:         → ("obelix", "", "")         remote obelix, its default project
//	obelix:gbrain:d* → ("obelix", "gbrain", "d*")
//	gbrain:*        → ("", "gbrain", "*")        project gbrain, all machines
//	g*:d*           → ("", "g*", "d*")
//	home            → ("", "home", "")
//
// A two-part reference is ambiguous — "a:b" is either remote:project or
// project:machine — and is resolved the same way rebindForReference resolves
// it for every other command: the leading part is a remote only if it names an
// ENROLLED one. remoteKnown is injected so this stays a pure function.
func splitListReference(arg string, remoteKnown func(string) bool) (remote, project, machine string) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", "", ""
	}
	parts := strings.Split(arg, ":")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	switch len(parts) {
	case 1:
		return "", parts[0], ""
	case 2:
		// "obelix:" (trailing colon, no machine part) always addresses a
		// remote — that shape has no other reading, so it does not depend on
		// the remote being resolvable from here.
		if parts[1] == "" || (remoteKnown != nil && remoteKnown(parts[0])) {
			return parts[0], parts[1], ""
		}
		return "", parts[0], parts[1]
	default:
		// Three or more parts: only remote:project:machine is meaningful. Extra
		// parts fold into the machine part, where validation rejects them.
		return parts[0], parts[1], strings.Join(parts[2:], ":")
	}
}

// listConfigForRemote rebinds the listing stores to another enrolled remote so
// `sc ls <remote>:...` reads that install without a durable switch. INCUS_CONF is
// pointed at that remote's incus config dir (the ADR-0021 shared dir for enrolled
// installs) for the duration and restored by the returned func; the project
// defaults to that remote's own pin when the arg carried none.
func listConfigForRemote(base commandConfig, remote string) (commandConfig, func(), error) {
	incusDir := scconfig.ResolveConfigPath(remote)
	if incusDir == "" {
		return base, func() {}, fmt.Errorf("no enrolled Sandcastle remote %q; run `sc remote list` to see installs", remote)
	}
	prev, had := os.LookupEnv("INCUS_CONF")
	os.Setenv("INCUS_CONF", incusDir)
	restore := func() {
		if had {
			os.Setenv("INCUS_CONF", prev)
		} else {
			os.Unsetenv("INCUS_CONF")
		}
	}
	sharedRemote := incusx.NewSharedRemote(remote).WithVerbose(os.Getenv("VERBOSE") == "1", os.Stderr)
	cfg := base
	cfg.adminConfig.Remote = remote
	if short := shortProjectName(scconfig.SharedIncusRemoteProject(remote), base.adminConfig.Tenant); short != "" {
		cfg.adminConfig.Project = short
	}
	cfg.tenantStore = incusx.NewTenantStoreForSharedRemote(sharedRemote)
	cfg.machineStore = incusx.NewHostOverrideManagerForSharedRemote(sharedRemote)
	return cfg, restore, nil
}

func listMachines(ctx context.Context, config commandConfig, request listMachinesRequest) (listPayload, error) {
	tenantName := strings.TrimSpace(config.adminConfig.Tenant)
	projectFilter := strings.TrimSpace(request.Project)
	if scopedTenant, scopedProject, ok := strings.Cut(projectFilter, "/"); ok {
		tenantName = strings.TrimSpace(scopedTenant)
		projectFilter = strings.TrimSpace(scopedProject)
	}
	ref, err := naming.ParseTenantRef(tenantName)
	if err != nil {
		return listPayload{}, fmt.Errorf("tenant is required; set SANDCASTLE_TENANT or local tenant config")
	}
	tenants, err := scopedListTenants(ctx, config, ref.Tenant)
	if err != nil {
		return listPayload{}, err
	}
	var summary tenant.Summary
	found := false
	for _, candidate := range tenants {
		if candidate.Tenant == ref.Tenant {
			summary = candidate
			found = true
			break
		}
	}
	if !found {
		return listPayload{}, fmt.Errorf("Sandcastle tenant %s not found", ref.String())
	}
	if config.machineStore == nil {
		return listPayload{}, fmt.Errorf("machine metadata store is not configured")
	}
	machineFilter := strings.TrimSpace(request.Machine)
	if machineFilter != "" {
		if err := validateNamePart("machine", machineFilter); err != nil {
			return listPayload{}, err
		}
	}
	if projectFilter != "" {
		if err := validateNamePart("project", projectFilter); err != nil {
			return listPayload{}, err
		}
		// A literal project must exist — a typo should say so rather than come
		// back as an empty listing. A project GLOB matching nothing is just an
		// empty set, the same as a machine glob matching nothing.
		if !naming.IsPattern(projectFilter) && !summaryHasProject(summary, projectFilter) {
			return listPayload{}, unknownProject{err: fmt.Errorf("Sandcastle project %s not found in tenant %s", projectFilter, summary.Tenant)}
		}
	} else if strings.TrimSpace(config.adminConfig.Project) != "" && !request.AllProjects {
		projectFilter = strings.TrimSpace(config.adminConfig.Project)
	}
	// The project filter — glob or not — is pushed into the store, which
	// resolves it against the tenant summary's project list (already in hand,
	// no Incus call) before querying anything. A glob that matches no project
	// therefore costs zero round trips.
	machines, unmanaged, err := listMachinesAndUnmanaged(ctx, config.machineStore, summary, projectFilter)
	if err != nil {
		return listPayload{}, err
	}
	filtered := make([]meta.Machine, 0, len(machines))
	for _, machine := range machines {
		if projectFilter != "" && !naming.MatchName(projectFilter, machine.Project) {
			continue
		}
		if machineFilter != "" && !naming.MatchName(machineFilter, machine.Name) {
			continue
		}
		filtered = append(filtered, machine)
	}
	// The unmanaged bucket carries no project, so only the machine-name filter
	// can apply to it.
	filteredUnmanaged := make([]machine.UnmanagedMachine, 0, len(unmanaged))
	for _, candidate := range unmanaged {
		if machineFilter != "" && !naming.MatchName(machineFilter, candidate.Name) {
			continue
		}
		filteredUnmanaged = append(filteredUnmanaged, candidate)
	}
	return listPayload{
		Tenant:         summary,
		Remote:         strings.TrimSpace(config.adminConfig.Remote),
		Project:        projectFilter,
		Machine:        machineFilter,
		AllProjects:    projectFilter == "",
		Machines:       filtered,
		Unmanaged:      filteredUnmanaged,
		UnmanagedCount: len(filteredUnmanaged),
	}, nil
}

// listMachinesAndUnmanaged reads the tenant's machines, scoped to projectFilter
// (a project name or a glob) when the caller has one. The scope is pushed down
// into the store rather than applied to the result: listing the whole tenant
// costs one Incus round trip per project, and `sc list` under a pinned project
// only ever shows one of them.
func listMachinesAndUnmanaged(ctx context.Context, store machine.Store, tenant tenant.Summary, projectFilter string) ([]meta.Machine, []machine.UnmanagedMachine, error) {
	if projectFilter != "" {
		if scoped, ok := store.(machine.ProjectScopedCombinedStore); ok {
			return scoped.ListMachinesAndUnmanagedInProject(ctx, tenant, projectFilter)
		}
	}
	if combined, ok := store.(machine.CombinedStore); ok {
		return combined.ListMachinesAndUnmanaged(ctx, tenant)
	}
	if projectFilter != "" {
		if scoped, ok := store.(machine.ProjectScopedStore); ok {
			machines, err := scoped.ListMachinesInProject(ctx, tenant, projectFilter)
			if err != nil {
				return nil, nil, err
			}
			unmanaged, err := listUnmanagedMachines(ctx, store, tenant)
			if err != nil {
				return nil, nil, err
			}
			return machines, unmanaged, nil
		}
	}
	machines, err := store.ListMachines(ctx, tenant)
	if err != nil {
		return nil, nil, err
	}
	unmanaged, err := listUnmanagedMachines(ctx, store, tenant)
	if err != nil {
		return nil, nil, err
	}
	return machines, unmanaged, nil
}

func listUnmanagedMachines(ctx context.Context, store machine.Store, tenant tenant.Summary) ([]machine.UnmanagedMachine, error) {
	unmanagedStore, ok := store.(machine.UnmanagedStore)
	if !ok {
		return nil, nil
	}
	machines, err := unmanagedStore.ListUnmanagedMachines(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("list unmanaged machines for %s: %w", tenant.Tenant, err)
	}
	return machines, nil
}

func summaryHasProject(summary tenant.Summary, name string) bool {
	for _, candidate := range summary.Projects {
		if candidate.Name == name {
			return true
		}
	}
	return false
}

// listContext names the remote (always) and project (when filtered to one) the
// listing covers, so output — especially an empty result — says WHICH install
// and project it looked in.
func listContext(result listPayload) string {
	parts := make([]string, 0, 3)
	if r := strings.TrimSpace(result.Remote); r != "" {
		parts = append(parts, fmt.Sprintf("remote %q", r))
	}
	if p := strings.TrimSpace(result.Project); p != "" && !result.AllProjects {
		label := "project"
		if naming.IsPattern(p) {
			label = "projects matching"
		}
		parts = append(parts, fmt.Sprintf("%s %q", label, p))
	}
	if m := strings.TrimSpace(result.Machine); m != "" {
		label := "machine"
		if naming.IsPattern(m) {
			label = "machines matching"
		}
		parts = append(parts, fmt.Sprintf("%s %q", label, m))
	}
	if len(parts) == 0 {
		return "the current install"
	}
	return strings.Join(parts, ", ")
}

// listMachinesAcrossRemotes sweeps every enrolled install matching pattern.
// Unlike the lifecycle verbs — which refuse to act on a partial sweep — a
// listing reports the installs it could not read as warnings and still shows
// what the others returned: a broken install should not blind you to the rest.
func listMachinesAcrossRemotes(ctx context.Context, config commandConfig, fanout remoteFanout, pattern string, request listMachinesRequest) (multiListPayload, error) {
	payload := multiListPayload{RemotePattern: pattern}
	var projectMissing error
	err := fanout.forEachRemoteScope(config, pattern, func(remote string, scoped commandConfig) error {
		result, err := listMachines(ctx, scoped, request)
		if err != nil {
			// An install that simply does not have the named project has
			// nothing to contribute — installs have different project sets.
			var missing unknownProject
			if errors.As(err, &missing) {
				projectMissing = err
				return nil
			}
			return err
		}
		payload.Remotes = append(payload.Remotes, result)
		return nil
	})
	if err != nil {
		// No install answered at all — that is a failure, not a warning.
		if len(payload.Remotes) == 0 {
			return multiListPayload{}, err
		}
		payload.Warnings = strings.Split(err.Error(), "\n")
	}
	// The project existed on no install at all: that is a typo, not an empty set.
	if len(payload.Remotes) == 0 && projectMissing != nil {
		return multiListPayload{}, projectMissing
	}
	return payload, nil
}

// multiListContext names the sweep for the header line, so an empty result
// still says where it looked.
func multiListContext(payload multiListPayload) string {
	parts := []string{fmt.Sprintf("remotes matching %q", payload.RemotePattern)}
	if len(payload.Remotes) > 0 {
		first := payload.Remotes[0]
		if p := strings.TrimSpace(first.Project); p != "" && !first.AllProjects {
			label := "project"
			if naming.IsPattern(p) {
				label = "projects matching"
			}
			parts = append(parts, fmt.Sprintf("%s %q", label, p))
		}
		if m := strings.TrimSpace(first.Machine); m != "" {
			label := "machine"
			if naming.IsPattern(m) {
				label = "machines matching"
			}
			parts = append(parts, fmt.Sprintf("%s %q", label, m))
		}
	}
	return strings.Join(parts, ", ")
}

func formatMultiMachineList(payload multiListPayload) string {
	var builder strings.Builder
	for _, warning := range payload.Warnings {
		fmt.Fprintf(&builder, "warning: %s\n", warning)
	}
	total := 0
	for _, section := range payload.Remotes {
		total += len(section.Machines) + len(section.Unmanaged)
	}
	if total == 0 {
		fmt.Fprintf(&builder, "No Sandcastle machines found in %s.", multiListContext(payload))
		return strings.TrimRight(builder.String(), "\n")
	}
	fmt.Fprintf(&builder, "%s\n", multiListContext(payload))
	table := tabwriter.NewWriter(&builder, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "REMOTE\tPROJECT\tMACHINE\tTYPE\tFQDN\tIP\tCREATED\tSTATE")
	for _, section := range payload.Remotes {
		// The FQDN comes from each section's OWN tenant summary — installs have
		// different DNS suffixes, so one shared summary would mislabel rows.
		for _, machine := range section.Machines {
			state := "stopped"
			if machine.Running {
				state = "running"
			}
			fmt.Fprintf(
				table,
				"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				section.Remote,
				machine.Project,
				machine.Name,
				machineTypeCell(machine),
				machineFQDN(section.Tenant, machine),
				machine.PrivateIP,
				formatListCreatedAt(machine.CreatedAt),
				state,
			)
		}
		for _, unmanaged := range section.Unmanaged {
			state := unmanaged.Status
			if state == "" {
				if unmanaged.Running {
					state = "running"
				} else {
					state = "stopped"
				}
			}
			fmt.Fprintf(
				table,
				"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				section.Remote,
				"-",
				unmanaged.Name,
				machineTypeShort(unmanaged.Type),
				"-",
				displayValue(unmanaged.PrivateIP),
				formatListCreatedAt(unmanaged.CreatedAt),
				"unmanaged:"+state,
			)
		}
	}
	_ = table.Flush()
	return strings.TrimRight(builder.String(), "\n")
}

func formatMachineList(result listPayload) string {
	if len(result.Machines) == 0 && len(result.Unmanaged) == 0 {
		return "No Sandcastle machines found in " + listContext(result) + "."
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "%s\n", listContext(result))
	table := tabwriter.NewWriter(&builder, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "PROJECT\tMACHINE\tTYPE\tFQDN\tIP\tCREATED\tSTATE")
	for _, machine := range result.Machines {
		state := "stopped"
		if machine.Running {
			state = "running"
		}
		fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			machine.Project,
			machine.Name,
			machineTypeCell(machine),
			machineFQDN(result.Tenant, machine),
			machine.PrivateIP,
			formatListCreatedAt(machine.CreatedAt),
			state,
		)
	}
	for _, unmanaged := range result.Unmanaged {
		state := unmanaged.Status
		if state == "" {
			if unmanaged.Running {
				state = "running"
			} else {
				state = "stopped"
			}
		}
		fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			"-",
			unmanaged.Name,
			machineTypeShort(unmanaged.Type),
			"-",
			displayValue(unmanaged.PrivateIP),
			formatListCreatedAt(unmanaged.CreatedAt),
			"unmanaged:"+state,
		)
	}
	_ = table.Flush()
	return strings.TrimRight(builder.String(), "\n")
}

func displayValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

// machineTypeShort renders an Incus instance type as the compact table label:
// CT for containers, VM for virtual machines.
// machineTypeCell renders the listing's TYPE column. A bare machine is marked
// there rather than in a column of its own: it is a property of what the
// machine IS, and the mark is what tells a reader why `sc connect` opens an
// Incus exec session on that row and an SSH session on the others.
func machineTypeCell(m meta.Machine) string {
	if m.Bare {
		return machineTypeShort(m.Type) + " (bare)"
	}
	return machineTypeShort(m.Type)
}

func machineTypeShort(instanceType string) string {
	switch instanceType {
	case "virtual-machine":
		return "VM"
	case meta.MachineTypeContainer, "":
		return "CT"
	default:
		return instanceType
	}
}

func machineFQDN(tenant tenant.Summary, machine meta.Machine) string {
	suffix := strings.Trim(strings.TrimSpace(tenant.DNSSuffix), ".")
	if suffix == "" {
		suffix = strings.Trim(strings.TrimSpace(tenant.Tenant), ".")
	}
	if machine.Name == "" || machine.Project == "" || suffix == "" {
		return "-"
	}
	// The canonical Machine Private Hostname is <machine>.<project>.<suffix>
	// for every project (ADR-0018); the default project also answers at the
	// short alias, but the list teaches the canonical form.
	return machine.Name + "." + machine.Project + "." + suffix
}

func formatListCreatedAt(createdAt string) string {
	createdAt = strings.TrimSpace(createdAt)
	if createdAt == "" {
		return "-"
	}
	parsed, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return createdAt
	}
	return parsed.Local().Format("2006-01-02 15:04:05")
}

func formatTenantResources(result tenantResourcesPayload) string {
	var b strings.Builder
	t := result.Tenant
	fmt.Fprintf(&b, "tenant:   %s\n", t.Tenant)
	fmt.Fprintf(&b, "cidr:     %s\n", displayValue(t.PrivateCIDR))
	fmt.Fprintf(&b, "dns:      %s\n", displayValue(t.DNSSuffix))
	if len(t.Projects) > 0 {
		names := make([]string, 0, len(t.Projects))
		for _, p := range t.Projects {
			names = append(names, p.Name)
		}
		fmt.Fprintf(&b, "projects: %s\n", strings.Join(names, ", "))
	}
	if len(t.PublicRoutes) > 0 {
		routes := make([]string, 0, len(t.PublicRoutes))
		for _, r := range t.PublicRoutes {
			routes = append(routes, r.Hostname)
		}
		fmt.Fprintf(&b, "routes:   %s\n", strings.Join(routes, ", "))
	}
	if t.Tailscale.State != "" {
		fmt.Fprintf(&b, "tailscale: %s\n", t.Tailscale.State)
	}
	if len(result.Machines) == 0 && len(result.Unmanaged) == 0 {
		fmt.Fprintf(&b, "\nNo machines found.")
		return b.String()
	}
	fmt.Fprintln(&b)
	table := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "PROJECT\tMACHINE\tTYPE\tFQDN\tIP\tCREATED\tSTATE")
	for _, m := range result.Machines {
		state := "stopped"
		if m.Running {
			state = "running"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			m.Project, m.Name, machineTypeCell(m), machineFQDN(t, m), m.PrivateIP, formatListCreatedAt(m.CreatedAt), state)
	}
	for _, u := range result.Unmanaged {
		state := u.Status
		if state == "" {
			if u.Running {
				state = "running"
			} else {
				state = "stopped"
			}
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			"-", u.Name, machineTypeShort(u.Type), "-", displayValue(u.PrivateIP), formatListCreatedAt(u.CreatedAt), "unmanaged:"+state)
	}
	_ = table.Flush()
	return strings.TrimRight(b.String(), "\n")
}
