package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thieso2/sandcastle-incus/internal/route"
)

func newRouteCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "route",
		Short: "Manage public HTTP routes",
	}
	command.AddCommand(newRouteCreateCommand(config, opts))
	command.AddCommand(newRouteListCommand(config, opts))
	command.AddCommand(newRouteStatusCommand(config, opts))
	command.AddCommand(newRouteDeleteCommand(config, opts))
	return command
}

func newRouteCreateCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "create hostname [tenant/][project:]machine",
		Short: "Create a public HTTP route",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := route.PlanCreate(cmd.Context(), config.adminConfig, config.tenantStore, config.routeMachine, route.CreateRequest{
				Hostname:        args[0],
				TargetReference: args[1],
			})
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatRouteCreate(plan), plan)
			}
			if config.routes == nil {
				return fmt.Errorf("route broker executor is not configured")
			}
			if err := config.routes.Create(cmd.Context(), plan); err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatRouteCreate(plan), plan)
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

func newRouteStatusCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status hostname",
		Short: "Show a public HTTP route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := route.PlanStatus(config.adminConfig, route.StatusRequest{Hostname: args[0]})
			if err != nil {
				return err
			}
			if config.routes == nil {
				return fmt.Errorf("route broker executor is not configured")
			}
			result, err := config.routes.List(cmd.Context(), route.ListPlan{
				InfrastructureProject: plan.InfrastructureProject,
				RequiresBroker:        plan.RequiresBroker,
			})
			if err != nil {
				return err
			}
			for _, publicRoute := range result.Routes {
				if publicRoute.Hostname == plan.Hostname {
					return writeOutput(config.stdout, opts.output, formatRouteStatus(publicRoute), publicRoute)
				}
			}
			return fmt.Errorf("public route %s not found", plan.Hostname)
		},
	}
}

func newRouteDeleteCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var yes bool
	var dryRun bool
	command := &cobra.Command{
		Use:   "delete hostname",
		Short: "Delete a public HTTP route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := route.PlanDelete(config.adminConfig, route.DeleteRequest{Hostname: args[0]})
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(config.stdout, opts.output, formatRouteDelete(plan), plan)
			}
			if !yes {
				confirmed, err := confirmMissingYes(config, "Delete route "+plan.Hostname+"?", "refusing to delete route without --yes")
				if err != nil {
					return err
				}
				if !confirmed {
					return nil
				}
			}
			if config.routes == nil {
				return fmt.Errorf("route broker executor is not configured")
			}
			if err := config.routes.Delete(cmd.Context(), plan); err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatRouteDelete(plan), plan)
		},
	}
	command.Flags().BoolVar(&yes, "yes", false, "confirm route deletion")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render the route delete plan without contacting the route broker")
	return command
}

func formatRouteCreate(plan route.CreatePlan) string {
	output := fmt.Sprintf("Route: %s -> %s:%d", plan.Hostname, plan.TargetReference, plan.RoutePort)
	if plan.DNSProof.Required {
		output += fmt.Sprintf("\nDNS proof: %s must resolve to %s", plan.DNSProof.Hostname, plan.DNSProof.ExpectedTarget)
	}
	return output
}

func formatRouteDelete(plan route.DeletePlan) string {
	return fmt.Sprintf("Delete route: %s", plan.Hostname)
}

func formatRouteList(result route.ListResult) string {
	if len(result.Routes) == 0 {
		return "No routes"
	}
	var output strings.Builder
	for index, route := range result.Routes {
		if index > 0 {
			output.WriteString("\n")
		}
		output.WriteString(formatRouteStatus(route))
	}
	return output.String()
}

func formatRouteStatus(publicRoute route.Route) string {
	return fmt.Sprintf("%s -> %s:%d", publicRoute.Hostname, publicRoute.TargetReference, publicRoute.RoutePort)
}
