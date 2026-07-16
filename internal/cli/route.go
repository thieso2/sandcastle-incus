package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	authapp "github.com/thieso2/sandcastle-incus/internal/authapp"
)

// newRouteCommand assembles `sc route`: publish a Machine's local port to the
// public Internet through the Auth App's Caddy (Spec #111). Available only on
// installs configured with public ingress.
func newRouteCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:          "route",
		Short:        "Publish a machine's local port to the public Internet",
		SilenceUsage: true,
	}
	command.AddCommand(newRoutePublishCommand(config, opts))
	command.AddCommand(newRouteListCommand(config, opts))
	command.AddCommand(newRouteStatusCommand(config, opts))
	command.AddCommand(newRouteDeleteCommand(config, opts))
	return command
}

func newRoutePublishCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var port int
	var name string
	var hostname string
	var dryRun bool
	command := &cobra.Command{
		Use:   "publish [[remote:]project:]machine --port <n>",
		Short: "Publish a machine's local port to the public Internet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if port <= 0 {
				return fmt.Errorf("--port is required (the backend port inside the machine, e.g. --port 3000)")
			}
			summary, err := requireV2Tenant(cmd.Context(), config)
			if err != nil {
				return err
			}
			project, machine, err := resolveV2MachineReference(summary, args[0], config.adminConfig.Project)
			if err != nil {
				return err
			}
			if dryRun {
				plan := map[string]any{
					"tenant": summary.Tenant, "project": project, "machine": machine,
					"backendPort": port, "url": "https://" + previewRouteHostname(config, summary.Tenant, name, hostname, machine),
					"dryRun": true,
				}
				return writeOutput(config.stdout, opts.output, formatRoutePlan(machine, port, plan["url"].(string)), plan)
			}
			client, err := routeClient(config)
			if err != nil {
				return err
			}
			view, err := client.PublishRoute(cmd.Context(), authapp.RoutePublishRequest{
				Tenant:      summary.Tenant,
				Project:     project,
				Machine:     machine,
				BackendPort: port,
				Name:        strings.TrimSpace(name),
				Hostname:    strings.TrimSpace(hostname),
			})
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatRoutePublished(view), view)
		},
	}
	command.Flags().IntVar(&port, "port", 0, "backend port inside the machine (required), e.g. 3000")
	command.Flags().StringVar(&name, "name", "", "subdomain label; defaults to the machine name")
	command.Flags().StringVar(&hostname, "hostname", "", "custom public hostname instead of the auto-subdomain (CNAME it onto the auth host yourself)")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "show the plan without publishing")
	return command
}

// previewRouteHostname derives the public hostname a publish would produce, for
// --dry-run. Mirrors the server's rule: a custom --hostname verbatim, else
// <label>.<tenant>.<auth-hostname> with the label defaulting to the machine name.
func previewRouteHostname(config commandConfig, tenant, name, hostname, machine string) string {
	if custom := strings.TrimSpace(hostname); custom != "" {
		return strings.ToLower(custom)
	}
	label := strings.TrimSpace(name)
	if label == "" {
		label = machine
	}
	authHost := strings.Trim(commandAuthHostname(config, ""), ".")
	if authHost == "" {
		return strings.ToLower(label + "." + tenant)
	}
	return strings.ToLower(label + "." + tenant + "." + authHost)
}

func formatRoutePlan(machine string, port int, url string) string {
	return fmt.Sprintf("Would publish %s:%d  ->  %s\n(dry run; not published)\n", machine, port, url)
}

func newRouteListCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List published routes for the current tenant",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			summary, err := requireV2Tenant(cmd.Context(), config)
			if err != nil {
				return err
			}
			client, err := routeClient(config)
			if err != nil {
				return err
			}
			routes, err := client.ListRoutes(cmd.Context(), summary.Tenant)
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatRouteList(routes), map[string]any{"routes": routes})
		},
	}
}

func newRouteStatusCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status <hostname>",
		Short: "Show a published route's backend, certificate, and health",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := routeClient(config)
			if err != nil {
				return err
			}
			view, err := client.GetRouteStatus(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, formatRouteStatus(view), view)
		},
	}
}

func newRouteDeleteCommand(config commandConfig, opts *rootOptions) *cobra.Command {
	var yes bool
	command := &cobra.Command{
		Use:   "delete <hostname>",
		Short: "Delete a published route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hostname := strings.TrimSpace(args[0])
			confirmed, err := confirmMissingYes(config, "Delete route "+hostname+"?", "refusing to delete route without --yes")
			if err != nil {
				return err
			}
			if !confirmed {
				return nil
			}
			client, err := routeClient(config)
			if err != nil {
				return err
			}
			if err := client.DeleteRoute(cmd.Context(), hostname); err != nil {
				return err
			}
			return writeOutput(config.stdout, opts.output, "Deleted route "+hostname+"\n", map[string]any{"hostname": hostname, "status": "deleted"})
		},
	}
	command.Flags().BoolVar(&yes, "yes", false, "confirm route deletion")
	return command
}

func routeClient(config commandConfig) (authRouteClient, error) {
	if config.authRoutes != nil {
		return config.authRoutes, nil
	}
	if strings.TrimSpace(config.adminConfig.AuthToken) == "" {
		return nil, fmt.Errorf("CLI Auth Token is required; run sc login")
	}
	baseURL := commandAuthHostname(config, "")
	if baseURL == "" {
		return nil, fmt.Errorf("Auth Hostname is required; run sc login")
	}
	return authapp.DeviceClient{BaseURL: baseURL, AuthToken: strings.TrimSpace(config.adminConfig.AuthToken)}, nil
}

func formatRoutePublished(view authapp.RouteView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Published %s\n\n", view.Hostname)
	fmt.Fprintf(&b, "  %s   ->   %s:%d\n", view.URL, view.Machine, view.BackendPort)
	if view.Status == authapp.RouteStatusAwaitingDNS {
		fmt.Fprintf(&b, "  awaiting DNS/certificate — the certificate issues on first request once DNS points here.\n")
	}
	return b.String()
}

func formatRouteStatus(view authapp.RouteView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hostname:  %s\n", view.Hostname)
	fmt.Fprintf(&b, "URL:       %s\n", view.URL)
	fmt.Fprintf(&b, "Backend:   %s:%s:%d\n", view.Tenant, view.Machine, view.BackendPort)
	fmt.Fprintf(&b, "Status:    %s\n", view.Status)
	return b.String()
}

func formatRouteList(routes []authapp.RouteView) string {
	if len(routes) == 0 {
		return "No published routes.\n"
	}
	var b strings.Builder
	writer := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "HOSTNAME\tMACHINE\tPORT\tSTATUS")
	for _, route := range routes {
		fmt.Fprintf(writer, "%s\t%s\t%d\t%s\n", route.Hostname, route.Machine, route.BackendPort, route.Status)
	}
	writer.Flush()
	return b.String()
}
