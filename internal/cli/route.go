package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

func newRouteCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "route",
		Short: "Manage public HTTP routes",
	}
	command.AddCommand(newRouteAddCommand(config, opts))
	command.AddCommand(newRouteListCommand(config, opts))
	command.AddCommand(newRouteRemoveCommand(config, opts))
	return command
}

func newRouteAddCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "add hostname project/name",
		Short: "Plan a public HTTP route",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := route.PlanAdd(cmd.Context(), config.adminConfig, config.projectStore, config.routeSandbox, route.AddRequest{
				Hostname:        args[0],
				TargetReference: args[1],
			})
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatRouteAdd(plan), plan)
			}
			if config.routes == nil {
				return fmt.Errorf("route broker executor is not configured")
			}
			if err := config.routes.Add(cmd.Context(), plan); err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatRouteAdd(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the route plan without contacting the route broker")
	return command
}

func newRouteListCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List public HTTP routes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := route.PlanList(config.adminConfig)
			if err != nil {
				return err
			}
			if config.routes == nil {
				return fmt.Errorf("route broker executor is not configured")
			}
			result, err := config.routes.List(cmd.Context(), plan)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatRouteList(result), result)
		},
	}
}

func newRouteRemoveCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "rm hostname",
		Short: "Remove a public HTTP route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := route.PlanRemove(config.adminConfig, route.RemoveRequest{Hostname: args[0]})
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatRouteRemove(plan), plan)
			}
			if config.routes == nil {
				return fmt.Errorf("route broker executor is not configured")
			}
			if err := config.routes.Remove(cmd.Context(), plan); err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatRouteRemove(plan), plan)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the route removal plan without contacting the route broker")
	return command
}

func formatRouteAdd(plan route.AddPlan) string {
	output := fmt.Sprintf("Route: %s -> %s:%d", plan.Hostname, plan.TargetReference, plan.RoutePort)
	if plan.DNSProof.Required {
		output += fmt.Sprintf("\nDNS proof: %s must resolve to %s", plan.DNSProof.Hostname, plan.DNSProof.ExpectedTarget)
	}
	return output
}

func formatRouteRemove(plan route.RemovePlan) string {
	return fmt.Sprintf("Remove route: %s", plan.Hostname)
}

func formatRouteList(result route.ListResult) string {
	if len(result.Routes) == 0 {
		return "No routes"
	}
	output := ""
	for index, route := range result.Routes {
		if index > 0 {
			output += "\n"
		}
		output += fmt.Sprintf("%s -> %s:%d", route.Hostname, route.TargetReference, route.RoutePort)
	}
	return output
}
