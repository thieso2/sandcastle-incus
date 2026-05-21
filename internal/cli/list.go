package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	machine "github.com/thieso2/sandcastle-incus/internal/machine"
	"github.com/thieso2/sandcastle-incus/internal/meta"
	"github.com/thieso2/sandcastle-incus/internal/naming"
	project "github.com/thieso2/sandcastle-incus/internal/tenant"
)

type listPayload struct {
	Tenant           project.Summary            `json:"tenant"`
	Project          string                     `json:"project,omitempty"`
	AllProjects      bool                       `json:"allProjects"`
	IncludeUnmanaged bool                       `json:"includeUnmanaged"`
	Machines         []meta.Machine             `json:"machines"`
	Unmanaged        []machine.UnmanagedMachine `json:"unmanaged,omitempty"`
	UnmanagedCount   int                        `json:"unmanagedCount"`
}

type tenantListPayload struct {
	Tenants []project.Summary `json:"tenants"`
}

func newListCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var allProjects bool
	var includeUnmanaged bool
	command := &cobra.Command{
		Use:   "list [project]",
		Short: "List Sandcastle machines",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := listMachines(cmd.Context(), config, listMachinesRequest{
				Project:          optionalArg(args),
				AllProjects:      allProjects,
				IncludeUnmanaged: includeUnmanaged,
			})
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatMachineList(result), result)
		},
	}
	command.Flags().BoolVarP(&allProjects, "all-projects", "a", false, "list machines across all projects")
	command.Flags().BoolVarP(&includeUnmanaged, "include-unmanaged", "u", false, "include unmanaged Incus instances when tenant-wide")
	return command
}

func listProjects(ctx context.Context, store project.IncusProjectStore) ([]project.Summary, error) {
	return project.List(ctx, store)
}

func formatTenantList(tenants []project.Summary) string {
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
	Project          string
	AllProjects      bool
	IncludeUnmanaged bool
}

func optionalArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func listMachines(ctx context.Context, config commandConfig, request listMachinesRequest) (listPayload, error) {
	ref, err := naming.ParseTenantRef(config.adminConfig.Tenant)
	if err != nil {
		return listPayload{}, fmt.Errorf("tenant is required; set SANDCASTLE_TENANT or local tenant config")
	}
	tenants, err := listProjects(ctx, config.projectStore)
	if err != nil {
		return listPayload{}, err
	}
	var tenant project.Summary
	found := false
	for _, candidate := range tenants {
		if candidate.Tenant == ref.Tenant {
			tenant = candidate
			found = true
			break
		}
	}
	if !found {
		return listPayload{}, fmt.Errorf("Sandcastle tenant %s not found", ref.String())
	}
	if config.sandboxStore == nil {
		return listPayload{}, fmt.Errorf("machine metadata store is not configured")
	}
	projectFilter := strings.TrimSpace(request.Project)
	if projectFilter != "" {
		if err := naming.ValidateProjectName(projectFilter); err != nil {
			return listPayload{}, err
		}
		if !summaryHasProject(tenant, projectFilter) {
			return listPayload{}, fmt.Errorf("Sandcastle project %s not found in tenant %s", projectFilter, tenant.Tenant)
		}
	} else if strings.TrimSpace(config.adminConfig.Project) != "" && !request.AllProjects {
		projectFilter = strings.TrimSpace(config.adminConfig.Project)
	}
	machines, err := config.sandboxStore.ListMachines(ctx, tenant)
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
	unmanaged, err := listUnmanagedMachines(ctx, config.sandboxStore, tenant)
	if err != nil {
		return listPayload{}, err
	}
	includedUnmanaged := []machine.UnmanagedMachine(nil)
	if request.IncludeUnmanaged && projectFilter == "" {
		includedUnmanaged = unmanaged
	}
	return listPayload{
		Tenant:           tenant,
		Project:          projectFilter,
		AllProjects:      projectFilter == "",
		IncludeUnmanaged: request.IncludeUnmanaged,
		Machines:         filtered,
		Unmanaged:        includedUnmanaged,
		UnmanagedCount:   len(unmanaged),
	}, nil
}

func listUnmanagedMachines(ctx context.Context, store machine.Store, tenant project.Summary) ([]machine.UnmanagedMachine, error) {
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

func summaryHasProject(summary project.Summary, name string) bool {
	for _, candidate := range summary.Projects {
		if candidate.Name == name {
			return true
		}
	}
	return false
}

func formatMachineList(result listPayload) string {
	if len(result.Machines) == 0 && len(result.Unmanaged) == 0 {
		return fmt.Sprintf("No Sandcastle machines found. Unmanaged: %d", result.UnmanagedCount)
	}

	var builder strings.Builder
	for _, machine := range result.Machines {
		state := "stopped"
		if machine.Running {
			state = "running"
		}
		fmt.Fprintf(
			&builder,
			"%s\t%s\t%s\t%d\t%s\n",
			machine.Project,
			machine.Name,
			machine.PrivateIP,
			machine.AppPort,
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
			&builder,
			"%s\t%s\t%s\t%d\t%s\n",
			"-",
			unmanaged.Name,
			"",
			0,
			"unmanaged:"+state,
		)
	}
	fmt.Fprintf(&builder, "Unmanaged: %d", result.UnmanagedCount)
	return strings.TrimRight(builder.String(), "\n")
}
