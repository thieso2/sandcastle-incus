package cli

import (
	"context"
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
	Project        string                     `json:"project,omitempty"`
	AllProjects    bool                       `json:"allProjects"`
	Machines       []meta.Machine             `json:"machines"`
	Unmanaged      []machine.UnmanagedMachine `json:"unmanaged,omitempty"`
	UnmanagedCount int                        `json:"unmanagedCount"`
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
		Use:     "list [project]",
		Aliases: []string{"ls"},
		Short:   "List Sandcastle machines",
		Args:    cobra.MaximumNArgs(1),
		Long: `List Sandcastle machines in the current install's project.

The argument may carry a "<remote>:" prefix to list another enrolled install
without switching to it — e.g. "sc ls obelix:home" (project home on remote
obelix) or "sc ls obelix:" (that remote's default project). "sc remote list"
shows the enrolled remotes.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			remoteOverride, project := splitRemotePrefix(optionalArg(args))
			runCfg := config
			if remoteOverride != "" && remoteOverride != strings.TrimSpace(config.adminConfig.Remote) {
				scoped, restore, err := listConfigForRemote(config, remoteOverride)
				if err != nil {
					return err
				}
				defer restore()
				runCfg = scoped
			}
			result, err := listMachines(cmd.Context(), runCfg, listMachinesRequest{
				Project:     project,
				AllProjects: allProjects,
			})
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
	AllProjects bool
}

func optionalArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// splitRemotePrefix parses `sc ls` addressing: a leading "<remote>:" targets
// another enrolled install (the incus remote name), the rest is the project.
// "obelix:home" → ("obelix","home"); "obelix:" → ("obelix",""); "home" → ("","home").
func splitRemotePrefix(arg string) (remote, rest string) {
	arg = strings.TrimSpace(arg)
	if r, p, ok := strings.Cut(arg, ":"); ok {
		return strings.TrimSpace(r), strings.TrimSpace(p)
	}
	return "", arg
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
	if projectFilter != "" {
		if err := naming.ValidateProjectName(projectFilter); err != nil {
			return listPayload{}, err
		}
		if !summaryHasProject(summary, projectFilter) {
			return listPayload{}, fmt.Errorf("Sandcastle project %s not found in tenant %s", projectFilter, summary.Tenant)
		}
	} else if strings.TrimSpace(config.adminConfig.Project) != "" && !request.AllProjects {
		projectFilter = strings.TrimSpace(config.adminConfig.Project)
	}
	machines, unmanaged, err := listMachinesAndUnmanaged(ctx, config.machineStore, summary)
	if err != nil {
		return listPayload{}, err
	}
	filtered := make([]meta.Machine, 0, len(machines))
	for _, machine := range machines {
		if projectFilter != "" && machine.Project != projectFilter {
			continue
		}
		filtered = append(filtered, machine)
	}
	return listPayload{
		Tenant:         summary,
		Project:        projectFilter,
		AllProjects:    projectFilter == "",
		Machines:       filtered,
		Unmanaged:      unmanaged,
		UnmanagedCount: len(unmanaged),
	}, nil
}

func listMachinesAndUnmanaged(ctx context.Context, store machine.Store, tenant tenant.Summary) ([]meta.Machine, []machine.UnmanagedMachine, error) {
	if combined, ok := store.(machine.CombinedStore); ok {
		return combined.ListMachinesAndUnmanaged(ctx, tenant)
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

func formatMachineList(result listPayload) string {
	if len(result.Machines) == 0 && len(result.Unmanaged) == 0 {
		return "No Sandcastle machines found."
	}

	var builder strings.Builder
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
			machineTypeShort(machine.Type),
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
			m.Project, m.Name, machineTypeShort(m.Type), machineFQDN(t, m), m.PrivateIP, formatListCreatedAt(m.CreatedAt), state)
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
